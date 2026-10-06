// Package device 定义光子器件模型：端口、散射矩阵以及非理想参数
// （插入损耗、相位偏置）的固定作用规则。
//
// 非理想参数约定（对全部器件一致）：
//   - insertion_loss_db：插入损耗，单位 dB，振幅增益 g = 10^(-IL/20)，
//     即功率衰减 10^(-IL/10)。取值范围 [0, 60]。
//   - phase_bias_deg：相位偏置，单位度，以固定方式叠加到器件理想相位
//     响应上（见各器件说明）。取值范围 [-3600, 3600]。
//   - 分束器 split_ratio：交叉端口功率耦合比，范围 [0, 1]。
package device

import (
	"fmt"
	"math"
	"math/cmplx"
)

// 参数合法范围，越界即拒绝（netlist 校验阶段）。
const (
	MaxInsertionLossDB = 60.0
	MaxAbsPhaseDeg     = 3600.0
)

// Device 是一个 n 端口器件，给出其散射矩阵 S（b = S·a）。
type Device struct {
	ID     string
	Type   string
	Ports  []string // 端口名，索引即散射矩阵行列索引
	Matrix [][]complex128
}

// ParamBounds 返回该器件类型各参数的合法范围 [min, max]。
func ParamBounds(devType string) (map[string][2]float64, bool) {
	switch devType {
	case "waveguide":
		return map[string][2]float64{
			"length_um":         {0, 1e6},
			"neff":              {0.5, 10},
			"phase_deg":         {-MaxAbsPhaseDeg, MaxAbsPhaseDeg},
			"insertion_loss_db": {0, MaxInsertionLossDB},
			"phase_bias_deg":    {-MaxAbsPhaseDeg, MaxAbsPhaseDeg},
		}, true
	case "beamsplitter":
		return map[string][2]float64{
			"split_ratio":       {0, 1},
			"insertion_loss_db": {0, MaxInsertionLossDB},
			"phase_bias_deg":    {-MaxAbsPhaseDeg, MaxAbsPhaseDeg},
		}, true
	case "phaseshifter":
		return map[string][2]float64{
			"phase_deg":         {-MaxAbsPhaseDeg, MaxAbsPhaseDeg},
			"insertion_loss_db": {0, MaxInsertionLossDB},
			"phase_bias_deg":    {-MaxAbsPhaseDeg, MaxAbsPhaseDeg},
		}, true
	}
	return nil, false
}

// KnownType 报告器件类型是否受支持。
func KnownType(devType string) bool {
	_, ok := ParamBounds(devType)
	return ok
}

func lossGain(ilDB float64) float64 { return math.Pow(10, -ilDB/20) }

func deg2rad(d float64) float64 { return d * math.Pi / 180 }

func get(params map[string]float64, key string, def float64) float64 {
	if v, ok := params[key]; ok {
		return v
	}
	return def
}

// New 按类型与参数构建器件。wavelengthNm 用于波导物理相位计算。
// 参数必须先通过 netlist 校验；此处假定参数合法。
func New(id, devType string, params map[string]float64, wavelengthNm float64) (*Device, error) {
	switch devType {
	case "waveguide":
		return newWaveguide(id, params, wavelengthNm), nil
	case "beamsplitter":
		return newBeamsplitter(id, params), nil
	case "phaseshifter":
		return newPhaseshifter(id, params), nil
	}
	return nil, fmt.Errorf("unknown device type %q", devType)
}

// newWaveguide 波导：单进单出。
// 总相位 = 2π·neff·length/λ + phase_deg + phase_bias_deg；振幅乘 g。
func newWaveguide(id string, p map[string]float64, wavelengthNm float64) *Device {
	length := get(p, "length_um", 0)
	neff := get(p, "neff", 0)
	phase := 0.0
	if length != 0 && neff != 0 {
		phase = 2 * math.Pi * neff * length * 1000 / wavelengthNm
	}
	phase += deg2rad(get(p, "phase_deg", 0))
	phase += deg2rad(get(p, "phase_bias_deg", 0))
	g := lossGain(get(p, "insertion_loss_db", 0))
	s := complex(g, 0) * cmplx.Exp(complex(0, phase))
	return &Device{
		ID: id, Type: "waveguide", Ports: []string{"in", "out"},
		Matrix: [][]complex128{{0, s}, {s, 0}},
	}
}

// newPhaseshifter 移相器：out = in · g · exp(i·(phase_deg + phase_bias_deg))。
func newPhaseshifter(id string, p map[string]float64) *Device {
	phase := deg2rad(get(p, "phase_deg", 0)) + deg2rad(get(p, "phase_bias_deg", 0))
	g := lossGain(get(p, "insertion_loss_db", 0))
	s := complex(g, 0) * cmplx.Exp(complex(0, phase))
	return &Device{
		ID: id, Type: "phaseshifter", Ports: []string{"in", "out"},
		Matrix: [][]complex128{{0, s}, {s, 0}},
	}
}

// newBeamsplitter 分束器：端口 in1,in2,out1,out2。
// 理想散射子矩阵（in→out）：[[t, i·r],[i·r, t]]，t=sqrt(1-ratio)，r=sqrt(ratio)。
// 整体乘以损耗增益 g 与相位偏置 exp(i·bias)。
func newBeamsplitter(id string, p map[string]float64) *Device {
	ratio := get(p, "split_ratio", 0.5)
	t := math.Sqrt(1 - ratio)
	r := math.Sqrt(ratio)
	g := lossGain(get(p, "insertion_loss_db", 0))
	bias := cmplx.Exp(complex(0, deg2rad(get(p, "phase_bias_deg", 0))))
	k := complex(g, 0) * bias
	m := [][]complex128{
		{0, 0, k * complex(t, 0), k * complex(0, r)},
		{0, 0, k * complex(0, r), k * complex(t, 0)},
		{k * complex(t, 0), k * complex(0, r), 0, 0},
		{k * complex(0, r), k * complex(t, 0), 0, 0},
	}
	return &Device{
		ID: id, Type: "beamsplitter", Ports: []string{"in1", "in2", "out1", "out2"},
		Matrix: m,
	}
}
