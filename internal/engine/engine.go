package engine

import (
	"errors"
	"fmt"
	"math"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/netlist"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/optics"
)

// RunError 表示仿真执行阶段（非网表编译阶段）的错误。
type RunError struct {
	Reason string
	Msg    string
}

func (e *RunError) Error() string { return fmt.Sprintf("仿真错误[%s]: %s", e.Reason, e.Msg) }

// InputPort 指定一个外部输入端口的激励。未提及的输入端口按零场处理。
type InputPort struct {
	Name  string
	Field optics.Field
}

// PortResult 是单个外部输出端口的结果。
type PortResult struct {
	Name       string
	Field      optics.Field
	Power      float64
	Phase      float64
	PhaseValid bool // 零场相位无定义
}

// Result 是一次相干传播的完整结果。
type Result struct {
	WavelengthNm float64
	Inputs       []InputPort
	Outputs      []PortResult
	InputPower   float64
	OutputPower  float64
}

// RunOptions 指定本次传播的波长与外部激励。
type RunOptions struct {
	WavelengthNm float64
	Inputs       []InputPort
}

// Run 在已编译的光路上做一次相干传播。
// Circuit 编译后不可变，Run 不写任何共享状态，可被多个 goroutine 并发调用。
func Run(c *netlist.Circuit, opts RunOptions) (*Result, error) {
	if c == nil {
		return nil, &RunError{Reason: "nil_circuit", Msg: "光路未编译"}
	}
	wl := opts.WavelengthNm
	if math.IsNaN(wl) || math.IsInf(wl, 0) || wl <= 0 {
		return nil, &RunError{Reason: "invalid_wavelength", Msg: fmt.Sprintf("波长必须为正的有限值，实际 %v", wl)}
	}

	inputByName := make(map[string]optics.Field, len(opts.Inputs))
	for _, in := range opts.Inputs {
		if _, dup := inputByName[in.Name]; dup {
			return nil, &RunError{Reason: "duplicate_input", Msg: fmt.Sprintf("输入端口 %s 被重复指定激励", in.Name)}
		}
		inputByName[in.Name] = in.Field
	}

	_, _, outNames, outFields, err := c.Propagate(wl, inputByName)
	if err != nil {
		return nil, &RunError{Reason: "unknown_input", Msg: err.Error()}
	}

	res := &Result{WavelengthNm: wl, Inputs: append([]InputPort(nil), opts.Inputs...)}
	res.Outputs = make([]PortResult, len(outNames))
	for i, name := range outNames {
		f := outFields[i]
		phi, ok := optics.RelativePhase(f, f)
		res.Outputs[i] = PortResult{
			Name: name, Field: f, Power: optics.PowerOf(f),
			Phase: phi, PhaseValid: ok,
		}
	}
	for _, in := range opts.Inputs {
		res.InputPower += optics.PowerOf(in.Field)
	}
	for _, o := range res.Outputs {
		res.OutputPower += o.Power
	}
	return res, nil
}

// EnergyConservationError 是理想无损耗光路守恒校验失败时的误差明细。
type EnergyConservationError struct {
	InputPower  float64
	OutputPower float64
	AbsDelta    float64
	RelDelta    float64
	Tolerance   float64
}

func (e *EnergyConservationError) Error() string {
	return fmt.Sprintf("能量守恒校验失败: 输入功率 %.12g, 输出功率 %.12g, 绝对误差 %.3e, 相对误差 %.3e, 容差 %.0e",
		e.InputPower, e.OutputPower, e.AbsDelta, e.RelDelta, e.Tolerance)
}

// DefaultTolerance 是守恒/解析对比的默认容差（绝对与相对同时满足）。
const DefaultTolerance = 1e-12

// AssertEnergyConserved 校验总功率守恒，|Pin-Pout| 必须同时不超过 absTol 与 relTol*Pin。
func AssertEnergyConserved(r *Result, absTol, relTol float64) error {
	if absTol <= 0 || relTol <= 0 || math.IsNaN(absTol) || math.IsNaN(relTol) {
		return errors.New("容差必须为正数")
	}
	abs := math.Abs(r.InputPower - r.OutputPower)
	rel := 0.0
	if r.InputPower != 0 {
		rel = abs / r.InputPower
	}
	if abs <= absTol || rel <= relTol {
		return nil
	}
	return &EnergyConservationError{
		InputPower: r.InputPower, OutputPower: r.OutputPower,
		AbsDelta: abs, RelDelta: rel, Tolerance: math.Max(absTol, relTol),
	}
}
