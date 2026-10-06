// Package netlist 定义本地网表文件格式（JSON）、解析与严格校验。
//
// 网表校验拒绝一切不确定结构，拒绝原因以可区分的错误码给出：
//   - ErrUnknownDevice      未知器件类型
//   - ErrPortMismatch       端口不存在 / 端口数不匹配
//   - ErrDuplicateConnection 同一端点被重复接线
//   - ErrDanglingPort       器件端口或外部端口悬空
//   - ErrParamOutOfRange    参数越界或参数名不存在
//   - ErrDuplicateID        器件 ID 重复
//   - ErrUnknownParam       扫描覆盖引用了不存在的器件或参数
//
// 校验失败的网表不会被“按理想器件顶上”或静默修正，直接整体拒绝。
package netlist

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/lechandonga/photonic-circuit-simulator-go/device"
)

// 可区分的拒绝原因。
var (
	ErrUnknownDevice       = errors.New("unknown device type")
	ErrPortMismatch        = errors.New("port mismatch")
	ErrDuplicateConnection = errors.New("duplicate connection")
	ErrDanglingPort        = errors.New("dangling port")
	ErrParamOutOfRange     = errors.New("parameter out of range")
	ErrDuplicateID         = errors.New("duplicate device id")
	ErrUnknownParam        = errors.New("unknown parameter")
)

// ValidationError 携带错误码与具体上下文。
type ValidationError struct {
	Code   error
	Detail string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%v: %s", e.Code, e.Detail) }
func (e *ValidationError) Unwrap() error { return e.Code }

// Endpoint 网表中的一个端点：某器件的某端口。
// 外部端口使用保留器件名 "$input" / "$output"，端口名为外部端口名。
type Endpoint struct {
	Device string `json:"device"`
	Port   string `json:"port"`
}

// DeviceSpec 网表中的器件描述。
type DeviceSpec struct {
	ID     string             `json:"id"`
	Type   string             `json:"type"`
	Params map[string]float64 `json:"params,omitempty"`
}

// Connection 一条连接两个端点的连线。
type Connection struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`
}

// Netlist 完整网表。
type Netlist struct {
	Name        string       `json:"name"`
	Inputs      []string     `json:"inputs"`
	Outputs     []string     `json:"outputs"`
	Devices     []DeviceSpec `json:"devices"`
	Connections []Connection `json:"connections"`
}

// Parse 从 reader 解析并校验网表。
func Parse(r io.Reader) (*Netlist, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var nl Netlist
	if err := dec.Decode(&nl); err != nil {
		return nil, fmt.Errorf("netlist parse: %w", err)
	}
	if err := nl.Validate(); err != nil {
		return nil, err
	}
	return &nl, nil
}

// LoadFile 从本地文件加载网表。
func LoadFile(path string) (*Netlist, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

const (
	inputDevice  = "$input"
	outputDevice = "$output"
)

// Validate 对网表做完整严格校验。
func (n *Netlist) Validate() error {
	if len(n.Inputs) == 0 {
		return &ValidationError{ErrPortMismatch, "netlist has no input ports"}
	}
	if len(n.Outputs) == 0 {
		return &ValidationError{ErrPortMismatch, "netlist has no output ports"}
	}
	ids := map[string]*DeviceSpec{}
	for i := range n.Devices {
		d := &n.Devices[i]
		if d.ID == "" || d.ID == inputDevice || d.ID == outputDevice {
			return &ValidationError{ErrDuplicateID, fmt.Sprintf("invalid device id %q", d.ID)}
		}
		if _, dup := ids[d.ID]; dup {
			return &ValidationError{ErrDuplicateID, d.ID}
		}
		if !device.KnownType(d.Type) {
			return &ValidationError{ErrUnknownDevice, fmt.Sprintf("device %q type %q", d.ID, d.Type)}
		}
		if err := checkParams(d.ID, d.Type, d.Params); err != nil {
			return err
		}
		ids[d.ID] = d
	}
	// 端点 -> 端口全集，用于悬空检查。
	allPorts := map[Endpoint]bool{}
	for i := range n.Devices {
		d := &n.Devices[i]
		for _, p := range portsOf(d.Type) {
			allPorts[Endpoint{d.ID, p}] = true
		}
	}
	for _, name := range n.Inputs {
		allPorts[Endpoint{inputDevice, name}] = true
	}
	for _, name := range n.Outputs {
		allPorts[Endpoint{outputDevice, name}] = true
	}

	connected := map[Endpoint]bool{}
	for _, c := range n.Connections {
		for _, ep := range []Endpoint{c.From, c.To} {
			if !allPorts[ep] {
				return &ValidationError{ErrPortMismatch,
					fmt.Sprintf("endpoint %s:%s does not exist", ep.Device, ep.Port)}
			}
			if connected[ep] {
				return &ValidationError{ErrDuplicateConnection,
					fmt.Sprintf("endpoint %s:%s connected more than once", ep.Device, ep.Port)}
			}
			connected[ep] = true
		}
		if c.From == c.To {
			return &ValidationError{ErrDuplicateConnection,
				fmt.Sprintf("endpoint %s:%s connected to itself", c.From.Device, c.From.Port)}
		}
	}
	// 悬空检查：每个端口必须恰好被连接一次。
	var dangling []string
	for ep := range allPorts {
		if !connected[ep] {
			dangling = append(dangling, ep.Device+":"+ep.Port)
		}
	}
	if len(dangling) > 0 {
		sort.Strings(dangling)
		return &ValidationError{ErrDanglingPort, fmt.Sprintf("unconnected ports: %v", dangling)}
	}
	return nil
}

func portsOf(devType string) []string {
	switch devType {
	case "waveguide", "phaseshifter":
		return []string{"in", "out"}
	case "beamsplitter":
		return []string{"in1", "in2", "out1", "out2"}
	}
	return nil
}

func checkParams(id, devType string, params map[string]float64) error {
	bounds, _ := device.ParamBounds(devType)
	for name, v := range params {
		b, ok := bounds[name]
		if !ok {
			return &ValidationError{ErrUnknownParam,
				fmt.Sprintf("device %q has no parameter %q", id, name)}
		}
		if v < b[0] || v > b[1] {
			return &ValidationError{ErrParamOutOfRange,
				fmt.Sprintf("device %q param %q=%v out of [%v, %v]", id, name, v, b[0], b[1])}
		}
	}
	return nil
}

// ParamRef 指向某器件的某参数（用于扫描覆盖）。
type ParamRef struct {
	Device string
	Param  string
}

// CheckOverride 校验一次参数覆盖是否合法（扫描点使用）。
func (n *Netlist) CheckOverride(devID, param string, value float64) error {
	for i := range n.Devices {
		d := &n.Devices[i]
		if d.ID != devID {
			continue
		}
		bounds, _ := device.ParamBounds(d.Type)
		b, ok := bounds[param]
		if !ok {
			return &ValidationError{ErrUnknownParam,
				fmt.Sprintf("device %q has no parameter %q", devID, param)}
		}
		if value < b[0] || value > b[1] {
			return &ValidationError{ErrParamOutOfRange,
				fmt.Sprintf("device %q param %q=%v out of [%v, %v]", devID, param, value, b[0], b[1])}
		}
		return nil
	}
	return &ValidationError{ErrUnknownParam, fmt.Sprintf("no device %q", devID)}
}

// WithOverrides 返回应用参数覆盖后的网表副本（深拷贝参数，不改原网表）。
func (n *Netlist) WithOverrides(overrides map[string]map[string]float64) *Netlist {
	cp := *n
	cp.Devices = make([]DeviceSpec, len(n.Devices))
	for i, d := range n.Devices {
		nd := d
		nd.Params = map[string]float64{}
		for k, v := range d.Params {
			nd.Params[k] = v
		}
		if ov, ok := overrides[d.ID]; ok {
			for k, v := range ov {
				nd.Params[k] = v
			}
		}
		cp.Devices[i] = nd
	}
	return &cp
}
