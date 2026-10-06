package sim_test

import (
	"math"
	"testing"

	"github.com/lechandonga/photonic-circuit-simulator-go/netlist"
	"github.com/lechandonga/photonic-circuit-simulator-go/sim"
)

const wavelength = 1550.0

func loadMZI(t *testing.T) *netlist.Netlist {
	t.Helper()
	nl, err := netlist.LoadFile("../testdata/mzi.json")
	if err != nil {
		t.Fatalf("load mzi netlist: %v", err)
	}
	return nl
}

// 理想 MZI：输出随臂相位 φ 的解析解为
// P(out0)=sin²(φ/2)，P(out1)=cos²(φ/2)，且总能量守恒。
func TestIdealMZI_MatchesAnalytic(t *testing.T) {
	t.Log("业务: 相干传播 / 理想 MZI 干涉臂相位扫描 vs 解析解")
	eng := &sim.Engine{}
	for _, phiDeg := range []float64{0, 30, 90, 137, 180, 270, 359} {
		nl := loadMZI(t).WithOverrides(map[string]map[string]float64{
			"ps": {"phase_deg": phiDeg},
		})
		res, err := eng.Simulate(nl, wavelength)
		if err != nil {
			t.Fatalf("phi=%v: %v", phiDeg, err)
		}
		phi := phiDeg * math.Pi / 180
		want0, want1 := math.Sin(phi/2)*math.Sin(phi/2), math.Cos(phi/2)*math.Cos(phi/2)
		if d := math.Abs(res.Outputs["out0"].Power - want0); d > 1e-12 {
			t.Errorf("phi=%v out0 power=%v want %v", phiDeg, res.Outputs["out0"].Power, want0)
		}
		if d := math.Abs(res.Outputs["out1"].Power - want1); d > 1e-12 {
			t.Errorf("phi=%v out1 power=%v want %v", phiDeg, res.Outputs["out1"].Power, want1)
		}
		if math.Abs(res.EnergyError) > sim.EnergyTolerance {
			t.Errorf("phi=%v energy error %g exceeds tol %g", phiDeg, res.EnergyError, sim.EnergyTolerance)
		}
		if res.Outputs["dump"].Power > 1e-24 {
			t.Errorf("phi=%v dump port should be dark, got %v", phiDeg, res.Outputs["dump"].Power)
		}
	}
}

// 理想 50:50 分束器：两输出各半功率，相对相位差 90°。
func TestIdealBeamsplitter(t *testing.T) {
	t.Log("业务: 相干传播 / 理想分束器分光比与相对相位")
	nl := loadMZI(t).WithOverrides(map[string]map[string]float64{
		"ps": {"phase_deg": 0},
	})
	// φ=0 时 out0 全暗、out1 全亮，验证干涉相消/相长。
	eng := &sim.Engine{}
	res, err := eng.Simulate(nl, wavelength)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outputs["out0"].Power > 1e-24 || math.Abs(res.Outputs["out1"].Power-1) > 1e-12 {
		t.Errorf("phi=0: got out0=%v out1=%v", res.Outputs["out0"].Power, res.Outputs["out1"].Power)
	}
}

// 含损耗波导：3 dB 插入损耗 => 功率减半（10^(-3/10)）。
func TestLossyWaveguide(t *testing.T) {
	t.Log("业务: 非理想参数 / 插入损耗对输出功率的固定作用规则")
	nl := loadMZI(t).WithOverrides(map[string]map[string]float64{
		"wg1": {"insertion_loss_db": 3},
	})
	eng := &sim.Engine{}
	res, err := eng.Simulate(nl, wavelength)
	if err != nil {
		t.Fatal(err)
	}
	if res.Lossless {
		t.Error("expected Lossless=false")
	}
	// 臂 1 振幅乘 10^(-3/20)，总输出功率必须小于输入功率。
	if res.OutputPower >= res.InputPower {
		t.Errorf("lossy circuit must dissipate: in=%v out=%v", res.InputPower, res.OutputPower)
	}
	g2 := math.Pow(10, -3.0/10)
	// φ=0 时 out0 = (g·t² − r²)·a，功率解析值：
	want0 := math.Pow(math.Sqrt(g2)*0.5-0.5, 2)
	if d := math.Abs(res.Outputs["out0"].Power - want0); d > 1e-12 {
		t.Errorf("lossy out0 power=%v want %v", res.Outputs["out0"].Power, want0)
	}
}

// 相位偏置：phase_bias_deg 与 phase_deg 等效叠加。
func TestPhaseBiasEquivalentToPhase(t *testing.T) {
	t.Log("业务: 非理想参数 / 相位偏置与相位参数等效叠加")
	eng := &sim.Engine{}
	// φ=90 通过 phase_deg
	nl1 := loadMZI(t).WithOverrides(map[string]map[string]float64{"ps": {"phase_deg": 90}})
	// φ=90 通过 phase_bias_deg
	nl2 := loadMZI(t).WithOverrides(map[string]map[string]float64{"ps": {"phase_bias_deg": 90}})
	r1, err1 := eng.Simulate(nl1, wavelength)
	r2, err2 := eng.Simulate(nl2, wavelength)
	if err1 != nil || err2 != nil {
		t.Fatalf("%v %v", err1, err2)
	}
	for _, out := range []string{"out0", "out1"} {
		if r1.Outputs[out] != r2.Outputs[out] {
			t.Errorf("%s: phase vs bias mismatch: %+v vs %+v", out, r1.Outputs[out], r2.Outputs[out])
		}
	}
}

// 波导物理相位：length+neff 决定的相位 2π·neff·L/λ 可复现。
func TestWaveguidePhysicalPhase(t *testing.T) {
	t.Log("业务: 器件模型 / 波导物理相位随波长确定")
	eng := &sim.Engine{}
	nl := loadMZI(t).WithOverrides(map[string]map[string]float64{
		"wg1": {"length_um": 100, "neff": 2.4},
	})
	r1, err := eng.Simulate(nl, wavelength)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := eng.Simulate(nl, wavelength)
	if err != nil {
		t.Fatal(err)
	}
	if r1.OutputPower != r2.OutputPower || r1.Outputs["out0"] != r2.Outputs["out0"] {
		t.Error("same input must give identical results")
	}
	if math.Abs(r1.EnergyError) > sim.EnergyTolerance {
		t.Errorf("lossless waveguide must conserve energy, err=%g", r1.EnergyError)
	}
}
