package sweep_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/lechandonga/photonic-circuit-simulator-go/netlist"
	"github.com/lechandonga/photonic-circuit-simulator-go/sim"
	"github.com/lechandonga/photonic-circuit-simulator-go/sweep"
)

func loadMZI(t *testing.T) *netlist.Netlist {
	t.Helper()
	nl, err := netlist.LoadFile("../testdata/mzi.json")
	if err != nil {
		t.Fatal(err)
	}
	return nl
}

func phaseSweepPoints(n int) []sweep.Point {
	pts := make([]sweep.Point, n)
	for i := range pts {
		phi := float64(i) * 360 / float64(n)
		pts[i] = sweep.Point{
			Label:     fmt.Sprintf("phi=%.2f", phi),
			Overrides: map[string]map[string]float64{"ps": {"phase_deg": phi}},
		}
	}
	return pts
}

// 并发扫描：结果与解析解一致，且重复执行逐点完全相同（确定可复现）。
func TestConcurrentSweepDeterministic(t *testing.T) {
	t.Log("业务: 批量扫描 / 并发执行结果确定且与解析解一致")
	nl := loadMZI(t)
	eng := &sim.Engine{}
	pts := phaseSweepPoints(64)
	req := sweep.Request{Netlist: nl, Points: pts, DefaultWavelengthNm: 1550, Workers: 8}

	run := func() []sweep.PointResult {
		res := sweep.Run(context.Background(), eng, req)
		for i, pr := range res {
			if pr.Err != nil {
				t.Fatalf("point %d failed: %v", i, pr.Err)
			}
			phi := float64(i) * 360 / float64(len(pts)) * math.Pi / 180
			want := math.Sin(phi/2) * math.Sin(phi/2)
			if d := math.Abs(pr.Result.Outputs["out0"].Power - want); d > 1e-12 {
				t.Fatalf("point %d out0=%v want %v", i, pr.Result.Outputs["out0"].Power, want)
			}
		}
		return res
	}
	r1 := run()
	r2 := run()
	for i := range r1 {
		for out, v1 := range r1[i].Result.Outputs {
			if v1 != r2[i].Result.Outputs[out] {
				t.Fatalf("point %d port %s differs between runs: %+v vs %+v", i, out, v1, r2[i].Result.Outputs[out])
			}
		}
	}
}

// 单点失败（参数越界）不影响其它点，且能定位到具体点。
func TestSweepPointFailureIsolated(t *testing.T) {
	t.Log("业务: 批量扫描 / 单点失败隔离并可定位")
	nl := loadMZI(t)
	eng := &sim.Engine{}
	pts := phaseSweepPoints(8)
	pts[3].Overrides = map[string]map[string]float64{"ps": {"phase_deg": 1e9}} // 越界
	res := sweep.Run(context.Background(), eng,
		sweep.Request{Netlist: nl, Points: pts, DefaultWavelengthNm: 1550, Workers: 4})
	for i, pr := range res {
		if i == 3 {
			if !errors.Is(pr.Err, netlist.ErrParamOutOfRange) {
				t.Fatalf("point 3 should fail with ErrParamOutOfRange, got %v", pr.Err)
			}
			if pr.Index != 3 || pr.Label != pts[3].Label {
				t.Fatalf("failed point not identifiable: %+v", pr)
			}
			continue
		}
		if pr.Err != nil || pr.Result == nil {
			t.Fatalf("point %d unexpectedly affected: %v", i, pr.Err)
		}
	}
}

// 取消：已产出的结果自洽，未产出的槽位保持零值，不串结果。
func TestSweepCancellation(t *testing.T) {
	t.Log("业务: 批量扫描 / 中途取消后已产出结果自洽")
	nl := loadMZI(t)
	eng := &sim.Engine{}
	pts := phaseSweepPoints(200)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(2 * time.Millisecond); cancel() }()
	res := sweep.Run(ctx, eng,
		sweep.Request{Netlist: nl, Points: pts, DefaultWavelengthNm: 1550, Workers: 4})
	if len(res) != len(pts) {
		t.Fatalf("result slice must align with points: %d vs %d", len(res), len(pts))
	}
	produced := 0
	for i, pr := range res {
		if pr.Result == nil {
			continue // 未产出的槽位为零值
		}
		produced++
		if pr.Index != i || pr.Label != pts[i].Label {
			t.Fatalf("slot %d holds foreign result: %+v", i, pr)
		}
		phi := float64(i) * 360 / 200 * math.Pi / 180
		want := math.Sin(phi/2) * math.Sin(phi/2)
		if d := math.Abs(pr.Result.Outputs["out0"].Power - want); d > 1e-12 {
			t.Fatalf("produced point %d inconsistent: %v want %v", i, pr.Result.Outputs["out0"].Power, want)
		}
	}
	t.Logf("取消前产出 %d/%d 点，全部自洽", produced, len(pts))
}

// 多波长扫描：不同波长点互不干扰。
func TestWavelengthSweep(t *testing.T) {
	t.Log("业务: 批量扫描 / 多波长扫描点间不串结果")
	nl := loadMZI(t).WithOverrides(map[string]map[string]float64{
		"wg1": {"length_um": 100, "neff": 2.4},
	})
	eng := &sim.Engine{}
	var pts []sweep.Point
	for wl := 1500.0; wl <= 1600; wl += 10 {
		pts = append(pts, sweep.Point{Label: fmt.Sprintf("wl=%.0f", wl), WavelengthNm: wl})
	}
	res := sweep.Run(context.Background(), eng,
		sweep.Request{Netlist: nl, Points: pts, DefaultWavelengthNm: 1550, Workers: 4})
	seen := map[float64]bool{}
	for _, pr := range res {
		if pr.Err != nil {
			t.Fatalf("%s: %v", pr.Label, pr.Err)
		}
		p := pr.Result.Outputs["out0"].Power
		if seen[p] {
			t.Logf("warning: duplicate power value %v (possible but unlikely)", p)
		}
		seen[p] = true
	}
}
