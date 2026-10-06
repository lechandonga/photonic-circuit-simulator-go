package scan

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/engine"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/netlist"
)

// Override 指定扫描点对器件参数的覆盖（键为 "deviceID.paramName"）。
// 未覆盖的器件沿用网表中的原始参数。
type Override struct {
	Device string
	Param  string
	Value  float64
}

// Point 是单个扫描点：同一份网表 + 一组参数覆盖 + 波长与激励。
type Point struct {
	Name         string
	WavelengthNm float64
	Overrides    []Override
	Inputs       []engine.InputPort
}

// PointResult 是单个扫描点的结果。Error 非空表示该点失败，不影响其它点。
type PointResult struct {
	Index  int
	Name   string
	Result *engine.Result
	Error  error
}

// Result 是一整批扫描的结果，按扫描点下标升序排列，保证跨重复运行一致。
type Result struct {
	Points    []PointResult
	Canceled  bool
	Completed int // 实际产出（成功或明确失败）的扫描点数
}

// Compiled 表示一份应用了覆盖参数并编译成功的光路。
type Compiled struct {
	Circuit *netlist.Circuit
}

// Cache 是并发安全的“覆盖参数 -> 编译光路”共享缓存。
// key 采用规范化字符串：网表名 + 排序后的覆盖项，杜绝不同扫描点串结果。
type Cache struct {
	mu    sync.Mutex
	store map[string]*netlist.Circuit
	hits  int
}

func NewCache() *Cache { return &Cache{store: map[string]*netlist.Circuit{}} }

// Hits 返回缓存命中次数（仅用于测试与可观测性）。
func (c *Cache) Hits() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

// Runner 在固定的一份网表原始 JSON 上执行批量扫描，可选共享编译缓存。
type Runner struct {
	rawNetlist []byte
	cache      *Cache
	sem        chan struct{}
}

// NewRunner 创建扫描执行器。maxWorkers<=0 时取 len(points) 上限内的并发。
func NewRunner(rawNetlist []byte, cache *Cache, maxWorkers int) *Runner {
	r := &Runner{rawNetlist: append([]byte(nil), rawNetlist...), cache: cache}
	if maxWorkers > 0 {
		r.sem = make(chan struct{}, maxWorkers)
	}
	return r
}

// Run 并发执行扫描。每个扫描点独立解析/编译（或命中独立 key 的缓存）与仿真，
// 单点失败被捕获到 PointResult.Error；ctx 取消后未开始的点标记为取消，
// 已产出（含成功与明确失败）的结果保持自洽且按 Index 排序。
func (r *Runner) Run(ctx context.Context, points []Point) (*Result, error) {
	if ctx == nil {
		return nil, fmt.Errorf("scan: context 不能为 nil")
	}
	if len(points) == 0 {
		return &Result{}, nil
	}
	workers := cap(r.sem)
	if workers == 0 || workers > len(points) {
		workers = len(points)
	}
	sem := r.sem
	if sem == nil {
		sem = make(chan struct{}, workers)
	}

	results := make([]PointResult, len(points))
	var wg sync.WaitGroup
	for i := range points {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				results[i] = PointResult{Index: i, Name: points[i].Name,
					Error: fmt.Errorf("扫描点[%d](%s)在开始前被取消: %w", i, points[i].Name, ctx.Err())}
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			// 拿到令牌后再次检查取消，避免取消后还做无用功。
			select {
			case <-ctx.Done():
				results[i] = PointResult{Index: i, Name: points[i].Name,
					Error: fmt.Errorf("扫描点[%d](%s)被取消: %w", i, points[i].Name, ctx.Err())}
				return
			default:
			}
			res, err := r.runOne(points[i])
			results[i] = PointResult{Index: i, Name: points[i].Name, Result: res, Error: err}
		}()
	}
	wg.Wait()

	out := &Result{Points: results}
	for _, pr := range results {
		if pr.Error == nil || !(errors.Is(pr.Error, context.Canceled)) {
			out.Completed++
		}
	}
	out.Canceled = ctx.Err() != nil
	sort.SliceStable(out.Points, func(i, j int) bool { return out.Points[i].Index < out.Points[j].Index })
	return out, nil
}

func (r *Runner) runOne(p Point) (*engine.Result, error) {
	c, err := r.compile(p)
	if err != nil {
		return nil, fmt.Errorf("扫描点(%s)编译失败: %w", p.Name, err)
	}
	res, err := engine.Run(c, engine.RunOptions{WavelengthNm: p.WavelengthNm, Inputs: p.Inputs})
	if err != nil {
		return nil, fmt.Errorf("扫描点(%s)仿真失败: %w", p.Name, err)
	}
	return res, nil
}

// compile 返回该扫描点专属的编译光路；相同覆盖集合才共享同一缓存项。
func (r *Runner) compile(p Point) (*netlist.Circuit, error) {
	key := cacheKey(r.rawNetlist, p.Overrides)
	if r.cache != nil {
		r.cache.mu.Lock()
		if c, ok := r.cache.store[key]; ok {
			r.cache.hits++
			r.cache.mu.Unlock()
			return c, nil
		}
		r.cache.mu.Unlock()
	}

	raw, err := applyOverrides(r.rawNetlist, p.Overrides)
	if err != nil {
		return nil, err
	}
	c, err := netlist.Parse(raw)
	if err != nil {
		return nil, err
	}
	if r.cache != nil {
		r.cache.mu.Lock()
		if existing, ok := r.cache.store[key]; ok {
			r.cache.hits++
			r.cache.mu.Unlock()
			return existing, nil
		}
		r.cache.store[key] = c
		r.cache.mu.Unlock()
	}
	return c, nil
}

func cacheKey(raw []byte, ovs []Override) string {
	type kv struct {
		k string
		v float64
	}
	items := make([]kv, len(ovs))
	for i, o := range ovs {
		items[i] = kv{fmt.Sprintf("%s.%s", o.Device, o.Param), o.Value}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].k < items[j].k })
	key := string(raw) + "|"
	for _, it := range items {
		key += fmt.Sprintf("%s=%.17g;", it.k, it.v)
	}
	return key
}
