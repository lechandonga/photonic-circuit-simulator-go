package engine_test

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/engine"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/netlist"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/optics"
)

const mziNetlist = `{
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

// 业务：理想 MZI 干涉臂相位扫描，与解析解 P_cross=cos²(Δφ/2)、
// P_bar=sin²(Δφ/2) 一致（两输出端在 Δφ=0 时交叉端得全部能量）。
func TestMZIPhaseScanAnalytic(t *testing.T) {
	phis := []float64{0, math.Pi / 4, math.Pi / 2, math.Pi, 3 * math.Pi / 2, 2 * math.Pi}
	for _, phi := range phis {
		cc, err := withPhase(mziNetlist, phi)
		if err != nil {
			t.Fatal(err)
		}
		res, err := engine.Run(cc, engine.RunOptions{
			WavelengthNm: 1550,
			Inputs:       []engine.InputPort{{Name: "s1.in0", Field: optics.NewField(1, 0)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pBar := res.Outputs[0].Power
		pCross := res.Outputs[1].Power
		wantBar := 0.5 * (1 - math.Cos(phi))
		wantCross := 0.5 * (1 + math.Cos(phi))
		if math.Abs(pBar-wantBar) > 1e-12 || math.Abs(pCross-wantCross) > 1e-12 {
			t.Fatalf("phi=%.4f: got bar=%.12f cross=%.12f, want bar=%.12f cross=%.12f",
				phi, pBar, pCross, wantBar, wantCross)
		}
		if err := engine.AssertEnergyConserved(res, 1e-12, 1e-12); err != nil {
			t.Fatalf("phi=%.4f: %v", phi, err)
		}
		t.Logf("业务[MZI 相位扫描] Δφ=%6.3f: P_bar=%.6f P_cross=%.6f (与解析解一致, 守恒)",
			phi, pBar, pCross)
	}
}

// 业务：理想无损耗任意配置（含双端注入）总能量守恒，容差 1e-12。
func TestEnergyConservationTolerance(t *testing.T) {
	c, _ := netlist.Parse([]byte(mziNetlist))
	res, err := engine.Run(c, engine.RunOptions{
		WavelengthNm: 1550,
		Inputs: []engine.InputPort{
			{Name: "s1.in0", Field: optics.NewField(0.6, 0.7)},
			{Name: "s1.in1", Field: optics.NewField(0.4, -2.1)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.AssertEnergyConserved(res, 1e-12, 1e-12); err != nil {
		t.Fatalf("业务[守恒]: %v", err)
	}
	t.Logf("业务[守恒容差]: Pin=%.12f Pout=%.12f, |Δ|=%.3e <= 1e-12",
		res.InputPower, res.OutputPower, math.Abs(res.InputPower-res.OutputPower))

	// 守恒校验必须真的能发现误差：构造一个容差更严的场景对比逻辑。
	bad := &engine.Result{InputPower: 1, OutputPower: 0.9}
	if err := engine.AssertEnergyConserved(bad, 1e-12, 1e-12); err == nil {
		t.Fatal("10% 功率损失不应通过守恒校验")
	}
}

// 业务：波导 + 分束器 + 移相器全部带损耗时，输出能量等于按固定 dB 规则手算的结果。
func TestLossyCircuitAnalytic(t *testing.T) {
	const doc = `{
  "name": "lossy",
  "devices": [
    {"id": "s1", "type": "splitter", "params": {"coupling_ratio": 0.5, "excess_loss_db": 0.5}},
    {"id": "wg", "type": "waveguide", "params": {"length_m": 0.01, "neff": 2.3, "loss_db_per_m": 200}},
    {"id": "ps", "type": "phaseshifter", "params": {"phase_rad": 0.3, "insertion_loss_db": 0.2}}
  ],
  "links": [["s1.out0", "wg.in"], ["wg.out", "ps.in"]],
  "inputs": ["s1.in0", "s1.in1"],
  "outputs": ["s1.out1", "ps.out"]
}`
	c, err := netlist.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	res, err := engine.Run(c, engine.RunOptions{
		WavelengthNm: 1550,
		Inputs:       []engine.InputPort{{Name: "s1.in0", Field: optics.NewField(1, 0)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 直通端 out0：经过 s1 的 0.5 额外损耗。
	wantCross := 0.5 * math.Pow(10, -0.5/10) // i*0.5 分束臂
	wantBarPath := 0.5 *
		math.Pow(10, -0.5/10) * // s1
		math.Pow(10, -(200*0.01)/10) * // 波导 2dB
		math.Pow(10, -0.2/10) // 移相器 0.2dB
	// outputs 顺序按网表声明：s1.out1(交叉), ps.out
	if math.Abs(res.Outputs[0].Power-wantCross) > 1e-12 {
		t.Fatalf("交叉臂功率 got %.12f want %.12f", res.Outputs[0].Power, wantCross)
	}
	if math.Abs(res.Outputs[1].Power-wantBarPath) > 1e-12 {
		t.Fatalf("损耗链路功率 got %.12f want %.12f", res.Outputs[1].Power, wantBarPath)
	}
	totalOut := res.OutputPower
	if totalOut >= res.InputPower {
		t.Fatalf("含损耗光路输出功率 %.12f 必须严格小于输入 1", totalOut)
	}
	t.Logf("业务[含损耗光路]: 交叉臂=%.6f 损耗链路=%.6f, 总输出=%.6f",
		res.Outputs[0].Power, res.Outputs[1].Power, totalOut)
}

// 业务：非法波长与未声明输入必须报错。
func TestRunValidation(t *testing.T) {
	c, _ := netlist.Parse([]byte(mziNetlist))
	if _, err := engine.Run(c, engine.RunOptions{WavelengthNm: 0}); err == nil {
		t.Fatal("零波长应被拒绝")
	}
	if _, err := engine.Run(c, engine.RunOptions{
		WavelengthNm: 1550,
		Inputs:       []engine.InputPort{{Name: "nope.in", Field: 1}},
	}); err == nil {
		t.Fatal("未声明输入应被拒绝")
	}
}

// withPhase 把 top 臂的相位替换为 phi 后重新编译（测试辅助）。
func withPhase(doc string, phi float64) (*netlist.Circuit, error) {
	patched := strings.Replace(doc,
		`{"id": "top", "type": "phaseshifter", "params": {"phase_rad": 0}}`,
		`{"id": "top", "type": "phaseshifter", "params": {"phase_rad": `+strconv.FormatFloat(phi, 'g', -1, 64)+`}}`,
		1)
	return netlist.Parse([]byte(patched))
}
