// Package sim 从校验过的网表构建光路并做相干光传播仿真。
//
// 方法：对每个器件端口建立散射方程 b_i = Σ_j S_ij·a_j，连线把两个
// 端口的出射/入射波等同起来，外部输入端口注入单位振幅，外部输出端口
// 吸收。得到关于全部器件端口出射振幅的线性方程组，用带部分主元的
// 高斯消元求解。主元选取规则固定（列内最大模），无随机性、无并发，
// 因此结果确定且可复现。
package sim

import (
	"errors"
	"fmt"
	"math"
	"math/cmplx"

	"github.com/lechandonga/photonic-circuit-simulator-go/device"
	"github.com/lechandonga/photonic-circuit-simulator-go/netlist"
)

// EnergyTolerance 理想无损耗光路能量守恒判定的默认容差：
// |Σ|out|² − Σ|in|²| ≤ EnergyTolerance。
const EnergyTolerance = 1e-9

// ErrEnergyNotConserved 无损耗配置下能量不守恒（超出容差）时报错。
var ErrEnergyNotConserved = errors.New("energy not conserved within tolerance")

// PortResult 一个输出端口的结果。
type PortResult struct {
	Power     float64 // |振幅|²
	PhaseRad  float64 // 相对相位（弧度，相对第一个输入端口的入射相位）
	Amplitude complex128
}

// Result 一次仿真的输出。
type Result struct {
	Outputs     map[string]PortResult // key 为外部输出端口名
	InputPower  float64
	OutputPower float64
	// Lossless 表示电路中所有器件均无插入损耗。
	Lossless bool
	// EnergyError = OutputPower - InputPower。
	EnergyError float64
}

// Engine 仿真引擎。无内部可变状态，可安全并发使用。
type Engine struct {
	// EnergyTol 能量守恒容差，0 表示使用 EnergyTolerance。
	EnergyTol float64
}

type endpoint struct{ dev, port string }

// Simulate 在给定波长（nm）下仿真网表，所有输入端口注入单位振幅、零相位。
func (e *Engine) Simulate(nl *netlist.Netlist, wavelengthNm float64) (*Result, error) {
	if wavelengthNm <= 0 {
		return nil, fmt.Errorf("invalid wavelength %v", wavelengthNm)
	}
	devs := make([]*device.Device, 0, len(nl.Devices))
	lossless := true
	for _, ds := range nl.Devices {
		d, err := device.New(ds.ID, ds.Type, ds.Params, wavelengthNm)
		if err != nil {
			return nil, err
		}
		if il, ok := ds.Params["insertion_loss_db"]; ok && il != 0 {
			lossless = false
		}
		devs = append(devs, d)
	}

	// 变量编号：每个器件端口一个出射波变量。
	varIndex := map[endpoint]int{}
	n := 0
	for _, d := range devs {
		for _, p := range d.Ports {
			varIndex[endpoint{d.ID, p}] = n
			n++
		}
	}
	devByID := map[string]*device.Device{}
	for _, d := range devs {
		devByID[d.ID] = d
	}
	portIdx := map[endpoint]int{}
	for _, d := range devs {
		for i, p := range d.Ports {
			portIdx[endpoint{d.ID, p}] = i
		}
	}

	// 连线映射：器件端点 -> 对端端点（器件或外部）。
	peer := map[endpoint]endpoint{}
	srcOf := map[endpoint]string{} // 器件端点 <- 外部输入名
	outOf := map[endpoint]string{} // 器件端点 -> 外部输出名
	for _, c := range nl.Connections {
		a := endpoint{c.From.Device, c.From.Port}
		b := endpoint{c.To.Device, c.To.Port}
		peer[a] = b
		peer[b] = a
	}
	isExt := func(ep endpoint) bool { return ep.dev == "$input" || ep.dev == "$output" }
	for ep, p := range peer {
		if isExt(p) {
			if p.dev == "$input" {
				srcOf[ep] = p.port
			} else {
				outOf[ep] = p.port
			}
		}
	}

	// 组装线性方程组 A·x = rhs。
	// 对器件端口 i：x_i − Σ_j S_ij·a_j = 0，
	// 其中 a_j = x_{peer(j)}（对端为器件）或注入振幅（对端为外部输入）。
	a := make([][]complex128, n)
	rhs := make([]complex128, n)
	for _, d := range devs {
		for i, p := range d.Ports {
			row := varIndex[endpoint{d.ID, p}]
			a[row] = make([]complex128, n)
			a[row][row] = 1
			for j, q := range d.Ports {
				s := d.Matrix[i][j]
				if s == 0 {
					continue
				}
				src := endpoint{d.ID, q}
				if inName, ok := srcOf[src]; ok {
					_ = inName
					rhs[row] += s * 1 // 单位振幅、零相位注入
				} else if _, ok := outOf[src]; ok {
					// 对端是输出端口：无入射波，贡献为 0。
				} else {
					a[row][varIndex[peer[src]]] -= s
				}
			}
		}
	}

	x, err := solve(a, rhs)
	if err != nil {
		return nil, err
	}

	res := &Result{Outputs: map[string]PortResult{}, Lossless: lossless}
	res.InputPower = float64(len(nl.Inputs))
	// 输出端口振幅 = 与之相连的器件端口的出射波。
	for ep, outName := range outOf {
		amp := x[varIndex[ep]]
		pr := PortResult{
			Power:     cmplx.Abs(amp) * cmplx.Abs(amp),
			PhaseRad:  cmplx.Phase(amp),
			Amplitude: amp,
		}
		res.Outputs[outName] = pr
		res.OutputPower += pr.Power
	}
	res.EnergyError = res.OutputPower - res.InputPower
	tol := e.EnergyTol
	if tol == 0 {
		tol = EnergyTolerance
	}
	if lossless && math.Abs(res.EnergyError) > tol {
		return res, fmt.Errorf("%w: |error|=%g > tol=%g", ErrEnergyNotConserved, math.Abs(res.EnergyError), tol)
	}
	return res, nil
}

// solve 高斯消元，部分主元（列内最大模，行序确定），结果可复现。
func solve(a [][]complex128, b []complex128) ([]complex128, error) {
	n := len(b)
	for col := 0; col < n; col++ {
		piv := col
		max := cmplx.Abs(a[col][col])
		for r := col + 1; r < n; r++ {
			if v := cmplx.Abs(a[r][col]); v > max {
				max, piv = v, r
			}
		}
		if max < 1e-300 {
			return nil, fmt.Errorf("singular system at column %d (circuit may contain isolated loops)", col)
		}
		a[col], a[piv] = a[piv], a[col]
		b[col], b[piv] = b[piv], b[col]
		for r := col + 1; r < n; r++ {
			f := a[r][col] / a[col][col]
			if f == 0 {
				continue
			}
			for c := col; c < n; c++ {
				a[r][c] -= f * a[col][c]
			}
			b[r] -= f * b[col]
		}
	}
	x := make([]complex128, n)
	for i := n - 1; i >= 0; i-- {
		s := b[i]
		for j := i + 1; j < n; j++ {
			s -= a[i][j] * x[j]
		}
		x[i] = s / a[i][i]
	}
	return x, nil
}
