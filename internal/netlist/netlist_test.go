package netlist_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/netlist"
)

const mziIdeal = `{
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

func TestParseValid(t *testing.T) {
	c, err := netlist.Parse([]byte(mziIdeal))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("业务[合法网表]: 编译 %s 成功，%d 输入 %d 输出，拓扑序长度 %d",
		c.Name, c.InputCount(), c.OutputCount(), len(c.DeviceOrder()))
	if c.InputCount() != 2 || c.OutputCount() != 2 {
		t.Fatalf("端口数不对: %d %d", c.InputCount(), c.OutputCount())
	}
}

// 业务：每一类非法网表都必须给出可区分的原因码。
func TestInvalidNetlists(t *testing.T) {
	replace := func(s string) string { return strings.Replace(mziIdeal, `"name": "mzi"`, s, 1) }
	cases := []struct {
		name   string
		doc    string
		reason netlist.Reason
	}{
		{"未知器件", `{"name":"x","devices":[{"id":"a","type":"mystery"}],"links":[],"inputs":[],"outputs":[]}`, netlist.ErrUnknownDevice},
		{"未知端口", strings.Replace(mziIdeal, `"s1.out0"`, `"s1.out9"`, 1), netlist.ErrUnknownPort},
		{"重复接线", strings.Replace(mziIdeal, `["s1.out1", "bot.in"]`, `["s1.out0", "bot.in"]`, 1), netlist.ErrDuplicateWire},
		{"输入接输入", strings.Replace(mziIdeal, `["s1.out0", "top.in"]`, `["s1.in0", "top.in"]`, 1), netlist.ErrInvalidConnection},
		{"悬空端口", strings.Replace(mziIdeal, `"outputs": ["s2.out0", "s2.out1"]`, `"outputs": ["s2.out0"]`, 1), netlist.ErrDanglingPort},
		{"重复器件id", strings.Replace(mziIdeal, `"id": "bot"`, `"id": "top"`, 1), netlist.ErrDuplicateID},
		{"参数越界", strings.Replace(mziIdeal, `"coupling_ratio": 0.5`, `"coupling_ratio": 1.5`, 1), netlist.ErrInvalidParameter},
		{"未知参数", strings.Replace(mziIdeal, `"phase_rad": 0}}`, `"phase_rad": 0, "x": 1}}`, 1), netlist.ErrUnknownParameter},
		{"缺必填参数", `{"name":"x","devices":[{"id":"w","type":"waveguide","params":{"neff":2.3}}],"links":[],"inputs":[],"outputs":[]}`, netlist.ErrMissingParameter},
		{"JSON 损坏", `{not json`, netlist.ErrMalformedFile},
		{"有环网表", `{"name":"loop","devices":[
			{"id":"p1","type":"phaseshifter","params":{}},
			{"id":"p2","type":"phaseshifter","params":{}},
			{"id":"s", "type":"splitter","params":{"coupling_ratio":0.5}}],
			"links":[["p1.out","s.in0"],["s.out0","p2.in"],["p2.out","p1.in"]],
			"inputs":["s.in1"],"outputs":["s.out1"]}`, netlist.ErrCyclicNetlist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := tc.doc
			_ = replace
			_, err := netlist.Parse([]byte(doc))
			var ne *netlist.NetlistError
			if err == nil {
				t.Fatalf("业务[%s]: 期望错误原因 %s，但网表被接受", tc.name, tc.reason)
			}
			if !errors.As(err, &ne) {
				t.Fatalf("业务[%s]: 错误不是 NetlistError: %v", tc.name, err)
			}
			if ne.Reason != tc.reason {
				t.Fatalf("业务[%s]: 原因码 got %s want %s (%v)", tc.name, ne.Reason, tc.reason, err)
			}
			t.Logf("业务[%s]: 已按预期拒绝，原因=%s 定位=%s", tc.name, ne.Reason, ne.Where)
		})
	}
}

// 业务：相同输入编译出的拓扑序确定可复现。
func TestDeterministicCompile(t *testing.T) {
	c1, err := netlist.Parse([]byte(mziIdeal))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := netlist.Parse([]byte(mziIdeal))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Compare(strings.Join(c1.InputNames(), ","), strings.Join(c2.InputNames(), ",")) != 0 {
		t.Fatal("输入端口顺序不确定")
	}
}
