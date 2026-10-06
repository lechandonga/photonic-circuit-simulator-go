package optics

import (
	"math"
	"math/cmplx"
)

// Field 表示一条光路上的相干光复振幅。
// 约定：振幅模平方即功率；相位单位为弧度，按 exp(j*phi) 约定（j 为虚数单位）。
type Field = complex128

// NewField 用功率与相位（弧度）构造复振幅。功率必须非负。
func NewField(power, phase float64) Field {
	if power < 0 {
		panic("optics: negative field power")
	}
	amp := math.Sqrt(power)
	return complex(amp*math.Cos(phase), amp*math.Sin(phase))
}

// PowerOf 返回复振幅携带的功率 |a|^2。
func PowerOf(a Field) float64 {
	return real(a)*real(a) + imag(a)*imag(a)
}

// PhaseOf 返回复振幅的相位，范围 [-pi, pi)；零场相位无定义，按 0 处理。
func PhaseOf(a Field) float64 {
	if a == 0 {
		return 0
	}
	return cmplx.Phase(a)
}

// RelativePhase 返回 a 相对 ref 的相位差 (arg a - arg ref)，范围 [-pi, pi)。
// 零场相位无定义；若任一场为零场，返回 0 并由 ok=false 标示。
func RelativePhase(a, ref Field) (phi float64, ok bool) {
	if a == 0 || ref == 0 {
		return 0, false
	}
	phi = cmplx.Phase(a / ref)
	return phi, true
}
