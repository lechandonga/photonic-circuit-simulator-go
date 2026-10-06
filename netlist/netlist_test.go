package netlist_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lechandonga/photonic-circuit-simulator-go/netlist"
)

// 最小合法网表：一个波导直连输入输出。
const validJSON = `{
  "name": "wg",
  "inputs": ["in"],
  "outputs": ["out"],
  "devices": [{"id": "w", "type": "waveguide"}],
  "connections": [
    {"from": {"device": "$input", "port": "in"}, "to": {"device": "w", "port": "in"}},
    {"from": {"device": "w", "port": "out"}, "to": {"device": "$output", "port": "out"}}
  ]
}`

func TestValidNetlist(t *testing.T) {
	t.Log("业务: 网表校验 / 合法网表被接受")
	if _, err := netlist.Parse(strings.NewReader(validJSON)); err != nil {
		t.Fatalf("valid netlist rejected: %v", err)
	}
}

func TestInvalidNetlists(t *testing.T) {
	t.Log("业务: 网表校验 / 各类非法网表给出可区分拒绝原因")
	cases := []struct {
		name string
		json string
		code error
	}{
		{"未知器件", strings.Replace(validJSON, `"waveguide"`, `"laser"`, 1), netlist.ErrUnknownDevice},
		{"端口不存在", strings.Replace(validJSON, `"port": "in"}`,
			`"port": "nope"}`, 1), netlist.ErrPortMismatch},
		{"重复接线", `{
		  "name": "dup", "inputs": ["in"], "outputs": ["out"],
		  "devices": [{"id": "w", "type": "waveguide"}],
		  "connections": [
		    {"from": {"device": "$input", "port": "in"}, "to": {"device": "w", "port": "in"}},
		    {"from": {"device": "$input", "port": "in"}, "to": {"device": "w", "port": "out"}},
		    {"from": {"device": "w", "port": "out"}, "to": {"device": "$output", "port": "out"}}
		  ]}`, netlist.ErrDuplicateConnection},
		{"悬空端口", `{
		  "name": "dangle", "inputs": ["in"], "outputs": ["out"],
		  "devices": [{"id": "w", "type": "waveguide"}, {"id": "ps", "type": "phaseshifter"}],
		  "connections": [
		    {"from": {"device": "$input", "port": "in"}, "to": {"device": "w", "port": "in"}},
		    {"from": {"device": "w", "port": "out"}, "to": {"device": "$output", "port": "out"}}
		  ]}`, netlist.ErrDanglingPort},
		{"参数越界", strings.Replace(validJSON, `"type": "waveguide"`,
			`"type": "waveguide", "params": {"insertion_loss_db": 99}`, 1), netlist.ErrParamOutOfRange},
		{"未知参数名", strings.Replace(validJSON, `"type": "waveguide"`,
			`"type": "waveguide", "params": {"foo": 1}`, 1), netlist.ErrUnknownParam},
		{"器件ID重复", `{
		  "name": "dupid", "inputs": ["in"], "outputs": ["out"],
		  "devices": [{"id": "w", "type": "waveguide"}, {"id": "w", "type": "waveguide"}],
		  "connections": []}`, netlist.ErrDuplicateID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := netlist.Parse(strings.NewReader(tc.json))
			if err == nil {
				t.Fatalf("expected rejection with %v, got nil", tc.code)
			}
			if !errors.Is(err, tc.code) {
				t.Fatalf("expected code %v, got %v", tc.code, err)
			}
			t.Logf("拒绝原因: %v", err)
		})
	}
}

func TestWithOverridesDoesNotMutate(t *testing.T) {
	t.Log("业务: 参数覆盖 / 覆盖产生副本，原网表不被修改")
	nl, err := netlist.Parse(strings.NewReader(validJSON))
	if err != nil {
		t.Fatal(err)
	}
	nl2 := nl.WithOverrides(map[string]map[string]float64{"w": {"insertion_loss_db": 3}})
	if nl.Devices[0].Params["insertion_loss_db"] != 0 {
		t.Error("original netlist mutated")
	}
	if nl2.Devices[0].Params["insertion_loss_db"] != 3 {
		t.Error("override not applied to copy")
	}
	if err := nl.CheckOverride("w", "insertion_loss_db", 99); !errors.Is(err, netlist.ErrParamOutOfRange) {
		t.Errorf("override out of range should be rejected, got %v", err)
	}
	if err := nl.CheckOverride("ghost", "x", 1); !errors.Is(err, netlist.ErrUnknownParam) {
		t.Errorf("override unknown device should be rejected, got %v", err)
	}
}
