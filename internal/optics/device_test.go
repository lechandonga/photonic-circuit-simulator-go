package optics

import (
	"math"
	"math/cmplx"
	"strconv"
	"testing"
)

const testTol = 1e-12

func approxEq(t *testing.T, got, want, tol float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %.15g want %.15g (误差 %.3e)", label, got, want, math.Abs(got-want))
	}
}

// 业务：分束器 50/50 解析解（单端注入，两输出各一半，交叉臂 +pi/2）。
func TestSplitterIdealAnalytic(t *testing.T) {
	dev, err := NewDevice(KindSplitter, map[string]float64{"coupling_ratio": 0.5})
	if err != nil {
		t.Fatal(err)
	}
	out := dev.Apply([]Field{1, 0}, 1550)
	approxEq(t, PowerOf(out[0]), 0.5, testTol, "out0 power")
	approxEq(t, PowerOf(out[1]), 0.5, testTol, "out1 power")
	approxEq(t, cmplx.Phase(out[1]/out[0]), math.Pi/2, testTol, "cross 相对 bar 相位")

	// 功率守恒（任意 k）。
	for _, k := range []float64{0, 0.2, 0.37, 1} {
		d, _ := NewDevice(KindSplitter, map[string]float64{"coupling_ratio": k})
		o := d.Apply([]Field{NewField(0.7, 0.3), NewField(0.3, -1.1)}, 1550)
		pin := 0.7 + 0.3
		pout := PowerOf(o[0]) + PowerOf(o[1])
		approxEq(t, pout, pin, testTol, "splitter 守恒 k="+mathFormat(k))
	}
}

// 业务：分束器额外损耗按 dB 固定作用于幅度，损耗功率独立于分束比。
func TestSplitterExcessLoss(t *testing.T) {
	dev, err := NewDevice(KindSplitter, map[string]float64{"coupling_ratio": 0.5, "excess_loss_db": 3.0})
	if err != nil {
		t.Fatal(err)
	}
	out := dev.Apply([]Field{1, 0}, 1550)
	wantPower := math.Pow(10, -3.0/10) // 3dB 损耗 => 功率剩一半
	approxEq(t, PowerOf(out[0])+PowerOf(out[1]), wantPower, testTol, "3dB 损耗后总功率")
	approxEq(t, PowerOf(out[0]), wantPower/2, testTol, "out0 仍按 0.5 分束")
}

// 业务：波导相位 phi = 2*pi*neff*L/lambda，损耗按 loss_db_per_m*L 计 dB。
func TestWaveguideAnalytic(t *testing.T) {
	neff, length, wlNm := 2.3, 0.01, 1550.0
	dev, err := NewDevice(KindWaveguide, map[string]float64{"length_m": length, "neff": neff})
	if err != nil {
		t.Fatal(err)
	}
	out := dev.Apply([]Field{1}, wlNm)
	wantPhi := 2 * math.Pi * neff * length / (wlNm * 1e-9)
	gotPhi := cmplx.Phase(out[0])
	delta := math.Remainder(gotPhi-wantPhi, 2*math.Pi)
	// phi 巨大(~9.3e4)，浮点展开误差按相对量级放宽到 1e-10。
	approxEq(t, delta, 0, 1e-10, "波导相位")
	approxEq(t, PowerOf(out[0]), 1, testTol, "无损耗波导功率")

	lossy, _ := NewDevice(KindWaveguide, map[string]float64{
		"length_m": length, "neff": neff, "loss_db_per_m": 100,
	})
	o := lossy.Apply([]Field{1}, wlNm)
	approxEq(t, PowerOf(o[0]), math.Pow(10, -100*length/10), testTol, "波导损耗功率")
}

// 业务：移相器输出 = amp*exp(j*phi)*输入。
func TestPhaseShifter(t *testing.T) {
	dev, _ := NewDevice(KindPhaseShifter, map[string]float64{"phase_rad": math.Pi / 3, "insertion_loss_db": 1})
	in := NewField(2, 0.2)
	out := dev.Apply([]Field{in}, 1550)
	approxEq(t, PowerOf(out[0]), 2*math.Pow(10, -0.1), testTol, "移相器功率")
	delta := math.Remainder(cmplx.Phase(out[0])-(0.2+math.Pi/3), 2*math.Pi)
	approxEq(t, delta, 0, testTol, "移相器相位叠加")
}

// 业务：参数解析默认值、越界、未知参数与缺失必填参数必须被拒绝。
func TestResolveParams(t *testing.T) {
	p, err := ResolveParams(KindSplitter, nil)
	if err != nil {
		t.Fatal(err)
	}
	approxEq(t, p["coupling_ratio"], 0.5, 0, "splitter 默认分束比")

	bad := []struct {
		kind Kind
		p    map[string]float64
		msg  string
	}{
		{KindSplitter, map[string]float64{"coupling_ratio": 1.2}, "分束比越界"},
		{KindSplitter, map[string]float64{"excess_loss_db": -0.1}, "负损耗"},
		{KindSplitter, map[string]float64{"bogus": 1}, "未知参数"},
		{KindWaveguide, map[string]float64{"neff": 2.3}, "缺 length_m"},
		{KindWaveguide, map[string]float64{"length_m": 0, "neff": 2.3}, "零长度"},
		{Kind("modulator"), nil, "未知器件"},
	}
	for _, b := range bad {
		if _, err := ResolveParams(b.kind, b.p); err == nil {
			t.Fatalf("%s: 期望被拒绝但通过了: %v", b.msg, b.p)
		} else {
			t.Logf("拒绝用例[%s] -> %v", b.msg, err)
		}
	}
}

func mathFormat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
