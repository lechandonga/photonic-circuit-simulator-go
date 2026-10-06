package scan_test

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/engine"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/scan"
)

func mziPoints(n int) []scan.Point {
	pts := make([]scan.Point, n)
	for i := 0; i < n; i++ {
		phi := 2 * math.Pi * float64(i) / float64(n)
		pts[i] = scan.Point{
			Name:         "phi",
			WavelengthNm: 1550,
			Overrides:    []scan.Override{{Device: "top", Param: "phase_rad", Value: phi}},
			Inputs:       []engine.InputPort{{Name: "s1.in0", Field: 1}},
		}
	}
	return pts
}

// 业务：并发批量扫描，所有点独立产出、结果不串、按索引排序、与解析解一致。
func TestConcurrentBatchScan(t *testing.T) {
	const n = 48
	cache := scan.NewCache()
	runner := scan.NewRunner([]byte(mziDoc), cache, 8)

	runOnce := func() *scan.Result {
		res, err := runner.Run(context.Background(), mziPoints(n))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	r1 := runOnce()
	r2 := runOnce()

	if r1.Completed != n || r2.Completed != n {
		t.Fatalf("应完成全部 %d 点，实际 %d / %d", n, r1.Completed, r2.Completed)
	}
	for i := 0; i < n; i++ {
		if r1.Points[i].Index != i || r2.Points[i].Index != i {
			t.Fatalf("结果未按索引排序: 位置 %d 为 %d/%d", i, r1.Points[i].Index, r2.Points[i].Index)
		}
		if r1.Points[i].Error != nil || r2.Points[i].Error != nil {
			t.Fatalf("点 %d 不应失败: %v / %v", i, r1.Points[i].Error, r2.Points[i].Error)
		}
		phi := 2 * math.Pi * float64(i) / float64(n)
		wantBar := 0.5 * (1 - math.Cos(phi))
		got := r1.Points[i].Result.Outputs[0].Power
		if math.Abs(got-wantBar) > 1e-12 {
			t.Fatalf("点 %d 功率 got %.12f want %.12f", i, got, wantBar)
		}
		if math.Abs(r1.Points[i].Result.Outputs[0].Power-r2.Points[i].Result.Outputs[0].Power) > 0 {
			t.Fatalf("点 %d 两次运行结果不一致", i)
		}
	}
	if cache.Hits() == 0 {
		t.Fatal("相同覆盖集合第二次运行应命中共享缓存")
	}
	t.Logf("业务[并发批量扫描]: %d 点并发完成，两次运行逐点一致，缓存命中 %d 次", n, cache.Hits())
}

// 业务：不同扫描点参数不同，绝不允许串结果。用不同 phi 检查隔离。
func TestScanPointIsolation(t *testing.T) {
	cache := scan.NewCache()
	runner := scan.NewRunner([]byte(mziDoc), cache, 4)
	pts := []scan.Point{
		{Name: "a", WavelengthNm: 1550, Overrides: []scan.Override{{Device: "top", Param: "phase_rad", Value: 0}},
			Inputs: []engine.InputPort{{Name: "s1.in0", Field: 1}}},
		{Name: "b", WavelengthNm: 1550, Overrides: []scan.Override{{Device: "top", Param: "phase_rad", Value: math.Pi}},
			Inputs: []engine.InputPort{{Name: "s1.in0", Field: 1}}},
	}
	res, err := runner.Run(context.Background(), pts)
	if err != nil {
		t.Fatal(err)
	}
	pA := res.Points[0].Result.Outputs[0].Power
	pB := res.Points[1].Result.Outputs[0].Power
	if math.Abs(pA-0) > 1e-12 || math.Abs(pB-1) > 1e-12 {
		t.Fatalf("扫描点串结果: phi=0 bar=%.6f, phi=pi bar=%.6f", pA, pB)
	}
	t.Logf("业务[扫描点隔离]: phi=0 时 bar=%.6f, phi=pi 时 bar=%.6f", pA, pB)
}

// 业务：单点失败（越界参数/不存在器件/波长非法）不影响其它点，且能定位到具体点。
func TestScanPartialFailure(t *testing.T) {
	runner := scan.NewRunner([]byte(mziDoc), scan.NewCache(), 4)
	good := scan.Point{Name: "good", WavelengthNm: 1550,
		Overrides: []scan.Override{{Device: "top", Param: "phase_rad", Value: 0.5}},
		Inputs:    []engine.InputPort{{Name: "s1.in0", Field: 1}}}
	badParam := scan.Point{Name: "bad-param", WavelengthNm: 1550,
		Overrides: []scan.Override{{Device: "s1", Param: "coupling_ratio", Value: 2}}}
	badDevice := scan.Point{Name: "bad-device", WavelengthNm: 1550,
		Overrides: []scan.Override{{Device: "ghost", Param: "x", Value: 1}}}
	badWL := scan.Point{Name: "bad-wavelength", WavelengthNm: -1,
		Inputs: []engine.InputPort{{Name: "s1.in0", Field: 1}}}

	res, err := runner.Run(context.Background(), []scan.Point{good, badParam, badDevice, badWL})
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed != 4 {
		t.Fatalf("成功与明确失败都应产出，Completed=%d", res.Completed)
	}
	if res.Points[0].Error != nil {
		t.Fatalf("好点不应失败: %v", res.Points[0].Error)
	}
	for i := 1; i < 4; i++ {
		if res.Points[i].Error == nil || res.Points[i].Result != nil {
			t.Fatalf("坏点 %d 必须失败且不带结果", i)
		}
		t.Logf("业务[单点失败隔离] 点[%d](%s): %v", res.Points[i].Index, res.Points[i].Name, res.Points[i].Error)
	}
}

// 业务：中途取消后，未开始的点标记为取消，已产出的成功/失败结果保持自洽。
func TestScanCancellation(t *testing.T) {
	runner := scan.NewRunner([]byte(mziDoc), scan.NewCache(), 1) // 单 worker，取消时多数点排队
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := runner.Run(ctx, mziPoints(6))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Canceled {
		t.Fatal("结果应标记为已取消")
	}
	for _, pr := range res.Points {
		if !errors.Is(pr.Error, context.Canceled) {
			t.Fatalf("单 worker 下取消后所有点应带 context.Canceled，点 %s: %v", pr.Name, pr.Error)
		}
	}
	t.Logf("业务[扫描取消]: %d 点均自洽地标记为取消，无部分脏结果", len(res.Points))
}

// 业务：多批扫描共享同一 Runner/缓存并发执行，彼此不串结果（竞态检测重点）。
func TestConcurrentBatchesShareCache(t *testing.T) {
	cache := scan.NewCache()
	runner := scan.NewRunner([]byte(mziDoc), cache, 4)
	var wg sync.WaitGroup
	for b := 0; b < 6; b++ {
		wg.Add(1)
		go func(seed float64) {
			defer wg.Done()
			pts := mziPoints(12)
			for i := range pts {
				pts[i].Overrides[0].Value += seed
			}
			res, err := runner.Run(context.Background(), pts)
			if err != nil {
				t.Error(err)
				return
			}
			for _, pr := range res.Points {
				if pr.Error != nil {
					t.Errorf("批次偏移 %.1f 点 %s 失败: %v", seed, pr.Name, pr.Error)
				}
			}
		}(float64(b) * 0.017)
	}
	wg.Wait()
	t.Logf("业务[共享缓存并发批次]: 6 批全部独立完成，缓存命中 %d 次", cache.Hits())
}

const mziDoc = `{
  "name": "mzi",
  "devices": [
    {"id": "s1", "type": "splitter", "params": {"coupling_ratio": 0.5}},
    {"id": "top", "type": "phaseshifter", "params": {"phase_rad": 0}},
    {"id": "bot", "type": "phaseshifter", "params": {"phase_rad": 0}},
    {"id": "s2", "type": "splitter", "params": {"coupling_ratio": 0.5}}
  ],
  "links": [
    ["s1.out0", "top.in"], ["s1.out1", "bot.in"],
    ["top.out", "s2.in0"], ["bot.out", "s2.in1"]
  ],
  "inputs": ["s1.in0", "s1.in1"],
  "outputs": ["s2.out0", "s2.out1"]
}`
