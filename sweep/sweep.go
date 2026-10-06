// Package sweep 提供同一光路的多参数/多波长批量扫描，并发执行且
// 结果确定、可复现、可取消。
//
// 设计要点：
//   - 每个扫描点在独立的网表副本上仿真，任务间不共享任何可变状态，
//     因此不会串结果；Engine 本身无状态，可安全共享。
//   - 结果按扫描点索引写回固定槽位，与调度顺序无关，重复执行一致。
//   - 单点失败只记录在该点的 Err 中，不影响其它点。
//   - context 取消后停止派发新点，已产出的结果保持完整可用。
package sweep

import (
	"context"
	"runtime"
	"sync"

	"github.com/lechandonga/photonic-circuit-simulator-go/netlist"
	"github.com/lechandonga/photonic-circuit-simulator-go/sim"
)

// Point 一个扫描点：波长与器件参数覆盖。
type Point struct {
	Label        string
	WavelengthNm float64                       // <=0 时使用 DefaultWavelengthNm
	Overrides    map[string]map[string]float64 // deviceID -> param -> value
}

// PointResult 一个扫描点的结果，Err 非 nil 表示该点失败。
type PointResult struct {
	Index  int
	Label  string
	Result *sim.Result
	Err    error
}

// Request 一次批量扫描。
type Request struct {
	Netlist             *netlist.Netlist
	Points              []Point
	DefaultWavelengthNm float64
	// Workers 并发度，<=0 时取 runtime.NumCPU()。
	Workers int
}

// Run 并发执行批量扫描，返回与 Points 等长且按索引对齐的结果切片。
// 取消 ctx 会尽快停止派发；已完成的点结果保持有效。
func Run(ctx context.Context, eng *sim.Engine, req Request) []PointResult {
	results := make([]PointResult, len(req.Points))
	workers := req.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(req.Points) {
		workers = len(req.Points)
	}
	if workers == 0 {
		return results
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = runPoint(eng, req, i)
			}
		}()
	}
loop:
	for i := range req.Points {
		select {
		case <-ctx.Done():
			break loop
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	return results
}

func runPoint(eng *sim.Engine, req Request, i int) PointResult {
	p := req.Points[i]
	pr := PointResult{Index: i, Label: p.Label}
	wl := p.WavelengthNm
	if wl <= 0 {
		wl = req.DefaultWavelengthNm
	}
	// 先校验覆盖参数，越界/未知参数使该点失败，不影响其它点。
	for devID, params := range p.Overrides {
		for name, v := range params {
			if err := req.Netlist.CheckOverride(devID, name, v); err != nil {
				pr.Err = err
				return pr
			}
		}
	}
	nl := req.Netlist.WithOverrides(p.Overrides)
	res, err := eng.Simulate(nl, wl)
	pr.Result, pr.Err = res, err
	return pr
}
