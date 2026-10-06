package optics

import (
	"fmt"
	"math"
)

// Kind 标识器件类型。
type Kind string

const (
	KindWaveguide    Kind = "waveguide"
	KindSplitter     Kind = "splitter"
	KindPhaseShifter Kind = "phaseshifter"
)

// 器件端口标签约定：
//   waveguide/phaseshifter：in -> out
//   splitter：in0,in1 -> out0(bar/直通), out1(cross/交叉)
// 分束器散射矩阵采用常见的对称、幺正约定
//   S = [[t, i*r], [i*r, t]]，t=sqrt(1-k)，r=sqrt(k)
// 即交叉臂相对直通臂有 +pi/2 的固有相位。

var portLabels = map[string][]Port{
	string(KindWaveguide):    {{Name: "in", Dir: PortDirIn}, {Name: "out", Dir: PortDirOut}},
	string(KindSplitter):     {{Name: "in0", Dir: PortDirIn}, {Name: "in1", Dir: PortDirIn}, {Name: "out0", Dir: PortDirOut}, {Name: "out1", Dir: PortDirOut}},
	string(KindPhaseShifter): {{Name: "in", Dir: PortDirIn}, {Name: "out", Dir: PortDirOut}},
}

// PortDir 为端口方向：输入或输出（相对器件本身）。
type PortDir int

const (
	PortDirIn PortDir = iota
	PortDirOut
)

// Port 描述器件的一个物理端口。
type Port struct {
	Name string
	Dir  PortDir
}

// PortsOf 返回某类器件的固定端口表；未知类型返回 false。
func PortsOf(kind Kind) ([]Port, bool) {
	ports, ok := portLabels[string(kind)]
	return ports, ok
}

// Device 是参数已确定、可在给定波长下做相干散射的器件模型。
// Apply 的入参为全部输入端口的场（按端口表中输入端口顺序），
// 出参为全部输出端口的场（按端口表中输出端口顺序）。
type Device interface {
	Kind() Kind
	Apply(in []Field, wavelengthNm float64) []Field
}

// ParamSpec 描述一个器件参数的固定解析规则。
type ParamSpec struct {
	Name     string
	Required bool
	Default  float64
	// Check 返回越界/非法原因；nil 表示合法。
	Check func(v float64) error
}

func nonNegative(name string) func(float64) error {
	return func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return fmt.Errorf("参数 %s 必须为有限且非负的数，实际为 %v", name, v)
		}
		return nil
	}
}

func positive(name string) func(float64) error {
	return func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return fmt.Errorf("参数 %s 必须为有限且严格大于 0 的数，实际为 %v", name, v)
		}
		return nil
	}
}

func finite(name string) func(float64) error {
	return func(v float64) error {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("参数 %s 必须为有限数，实际为 %v", name, v)
		}
		return nil
	}
}

func ratioRange(name string) func(float64) error {
	return func(v float64) error {
		if math.IsNaN(v) || v < 0 || v > 1 {
			return fmt.Errorf("参数 %s 必须落在闭区间 [0,1] 内，实际为 %v", name, v)
		}
		return nil
	}
}

// ParamSpecs 返回各器件类型的参数表（固定规则，不允许网表传入表外参数）。
// 损耗参数统一以 dB 为单位且必须非负：透射幅度系数 = 10^(-loss_db/20)。
func ParamSpecs(kind Kind) ([]ParamSpec, bool) {
	switch kind {
	case KindWaveguide:
		return []ParamSpec{
			{Name: "length_m", Required: true, Check: positive("length_m")},
			{Name: "neff", Required: true, Check: finite("neff")},
			{Name: "loss_db_per_m", Default: 0, Check: nonNegative("loss_db_per_m")},
		}, true
	case KindSplitter:
		// coupling_ratio：进入交叉端口(out1)的功率比例 k；直通端口(out0)得 1-k。
		return []ParamSpec{
			{Name: "coupling_ratio", Default: 0.5, Check: ratioRange("coupling_ratio")},
			{Name: "excess_loss_db", Default: 0, Check: nonNegative("excess_loss_db")},
		}, true
	case KindPhaseShifter:
		// phase_rad：移相器施加的相位偏移（弧度），输出场 = 输入场 * amp * exp(j*phi)。
		return []ParamSpec{
			{Name: "phase_rad", Default: 0, Check: finite("phase_rad")},
			{Name: "insertion_loss_db", Default: 0, Check: nonNegative("insertion_loss_db")},
		}, true
	default:
		return nil, false
	}
}

// ResolveParams 按固定规则把原始参数表补齐默认值、拒绝缺失/越界/未知参数。
func ResolveParams(kind Kind, raw map[string]float64) (map[string]float64, error) {
	specs, ok := ParamSpecs(kind)
	if !ok {
		return nil, fmt.Errorf("未知器件类型 %q", kind)
	}
	out := make(map[string]float64, len(specs))
	known := make(map[string]bool, len(specs))
	for _, spec := range specs {
		known[spec.Name] = true
		v, present := raw[spec.Name]
		if !present {
			if spec.Required {
				return nil, fmt.Errorf("缺少必填参数 %s", spec.Name)
			}
			v = spec.Default
		}
		if spec.Check != nil {
			if err := spec.Check(v); err != nil {
				return nil, err
			}
		}
		out[spec.Name] = v
	}
	for name := range raw {
		if !known[name] {
			return nil, fmt.Errorf("未知参数 %q", name)
		}
	}
	return out, nil
}

// NewDevice 按类型与已解析参数构造器件；参数解析必须走 ResolveParams。
func NewDevice(kind Kind, params map[string]float64) (Device, error) {
	p, err := ResolveParams(kind, params)
	if err != nil {
		return nil, err
	}
	switch kind {
	case KindWaveguide:
		return &waveguide{length: p["length_m"], neff: p["neff"], lossPerM: p["loss_db_per_m"]}, nil
	case KindSplitter:
		return &splitter{k: p["coupling_ratio"], lossDB: p["excess_loss_db"]}, nil
	case KindPhaseShifter:
		return &phaseShifter{phi: p["phase_rad"], lossDB: p["insertion_loss_db"]}, nil
	default:
		return nil, fmt.Errorf("未知器件类型 %q", kind)
	}
}

func ampFromDB(lossDB float64) float64 {
	return math.Pow(10, -lossDB/20)
}

type waveguide struct {
	length   float64
	neff     float64
	lossPerM float64
}

func (d *waveguide) Kind() Kind { return KindWaveguide }

func (d *waveguide) Apply(in []Field, wavelengthNm float64) []Field {
	if wavelengthNm <= 0 || math.IsNaN(wavelengthNm) || math.IsInf(wavelengthNm, 0) {
		panic(fmt.Sprintf("optics: 波长非法 %v", wavelengthNm))
	}
	lambdaM := wavelengthNm * 1e-9
	phi := 2 * math.Pi * d.neff * d.length / lambdaM
	amp := math.Pow(10, -d.lossPerM*d.length/20)
	transfer := complex(amp*math.Cos(phi), amp*math.Sin(phi))
	return []Field{in[0] * transfer}
}

type splitter struct {
	k      float64
	lossDB float64
}

func (d *splitter) Kind() Kind { return KindSplitter }

func (d *splitter) Apply(in []Field, _ float64) []Field {
	t := math.Sqrt(1 - d.k)
	r := math.Sqrt(d.k)
	amp := ampFromDB(d.lossDB)
	a, b := in[0], in[1]
	j := complex(0, 1)
	out0 := complex(amp, 0) * (complex(t, 0)*a + j*complex(r, 0)*b)
	out1 := complex(amp, 0) * (j*complex(r, 0)*a + complex(t, 0)*b)
	return []Field{out0, out1}
}

type phaseShifter struct {
	phi    float64
	lossDB float64
}

func (d *phaseShifter) Kind() Kind { return KindPhaseShifter }

func (d *phaseShifter) Apply(in []Field, _ float64) []Field {
	amp := ampFromDB(d.lossDB)
	transfer := complex(amp*math.Cos(d.phi), amp*math.Sin(d.phi))
	return []Field{in[0] * transfer}
}
