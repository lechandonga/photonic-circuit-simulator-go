package netlist

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/optics"
)

// RawNetlist 是网表文件的 JSON 结构。
// 链路用 "deviceID.portName" 两端点表示；外部端口直接引用器件端口，
// 外部输入只能接器件输入端口，外部输出只能接器件输出端口。
type RawNetlist struct {
	Name    string      `json:"name"`
	Devices []RawDevice `json:"devices"`
	Links   [][2]string `json:"links"`
	Inputs  []string    `json:"inputs"`
	Outputs []string    `json:"outputs"`
}

// RawDevice 是器件的 JSON 结构。
type RawDevice struct {
	ID     string             `json:"id"`
	Type   string             `json:"type"`
	Params map[string]float64 `json:"params"`
}

type endpoint struct {
	dev  *compiledDevice
	port int // 在端口表中的下标
}

type compiledDevice struct {
	id     string
	kind   optics.Kind
	device optics.Device
	ports  []optics.Port
	// 每个端口的接线状态：nil 表示未接线；否则为对端端点
	wireTo   []*endpoint
	external []bool
}

// Circuit 是校验通过、拓扑序固定、可并发复用的不可变光路。
type Circuit struct {
	Name        string
	order       []*compiledDevice
	devices     map[string]*compiledDevice
	inputs      []*endpoint
	outputs     []*endpoint
	inputNames  []string
	outputNames []string
}

// InputCount / OutputCount 返回外部端口数。
func (c *Circuit) InputCount() int       { return len(c.inputs) }
func (c *Circuit) OutputCount() int      { return len(c.outputs) }
func (c *Circuit) InputNames() []string  { return append([]string(nil), c.inputNames...) }
func (c *Circuit) OutputNames() []string { return append([]string(nil), c.outputNames...) }

// DeviceOrder 返回拓扑序中的器件 id 序列（拷贝，便于可观测性与测试）。
func (c *Circuit) DeviceOrder() []string {
	out := make([]string, len(c.order))
	for i, cd := range c.order {
		out[i] = cd.id
	}
	return out
}

// LoadFile 从本地 JSON 文件读取并编译光路。
func LoadFile(path string) (*Circuit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse 解析并编译 JSON 网表。
func Parse(data []byte) (*Circuit, error) {
	var raw RawNetlist
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(&raw); err != nil {
		return nil, fail(ErrMalformedFile, "", "JSON 解析失败: %v", err)
	}
	if dec.More() {
		return nil, fail(ErrMalformedFile, "", "网表文件包含多余的 JSON 值")
	}
	if strings.TrimSpace(raw.Name) == "" {
		return nil, fail(ErrMalformedFile, "", "name 不能为空")
	}
	return compile(raw)
}

func compile(raw RawNetlist) (*Circuit, error) {
	if len(raw.Devices) == 0 {
		return nil, fail(ErrMalformedFile, "", "至少需要一个器件")
	}
	c := &Circuit{Name: raw.Name, devices: map[string]*compiledDevice{}}

	// 1) 构造器件：未知类型、重复 id、参数问题在此拒绝。
	for i := range raw.Devices {
		rd := raw.Devices[i]
		id := strings.TrimSpace(rd.ID)
		if id == "" {
			return nil, fail(ErrMalformedFile, fmt.Sprintf("devices[%d]", i), "器件 id 不能为空")
		}
		if _, exists := c.devices[id]; exists {
			return nil, fail(ErrDuplicateID, id, "器件 id 重复")
		}
		kind := optics.Kind(rd.Type)
		ports, ok := optics.PortsOf(kind)
		if !ok {
			return nil, fail(ErrUnknownDevice, id, "器件类型 %q 不被支持", rd.Type)
		}
		params := rd.Params
		if params == nil {
			params = map[string]float64{}
		}
		resolved, err := optics.ResolveParams(kind, params)
		if err != nil {
			reason := ErrInvalidParameter
			msg := err.Error()
			switch {
			case strings.HasPrefix(msg, "未知参数"):
				reason = ErrUnknownParameter
			case strings.HasPrefix(msg, "缺少必填参数"):
				reason = ErrMissingParameter
			}
			return nil, fail(reason, id, "%s", msg)
		}
		dev, _ := optics.NewDevice(kind, resolved)
		cd := &compiledDevice{
			id: id, kind: kind, device: dev, ports: ports,
			wireTo:   make([]*endpoint, len(ports)),
			external: make([]bool, len(ports)),
		}
		c.devices[id] = cd
	}

	// 2) 接内部链路。
	for i, link := range raw.Links {
		a, err := c.resolveEndpoint(link[0], fmt.Sprintf("links[%d][0]", i))
		if err != nil {
			return nil, err
		}
		b, err := c.resolveEndpoint(link[1], fmt.Sprintf("links[%d][1]", i))
		if err != nil {
			return nil, err
		}
		if a.dev == b.dev && a.port == b.port {
			return nil, fail(ErrInvalidConnection, link[0], "端口不能接自己")
		}
		if a.dev.ports[a.port].Dir == b.dev.ports[b.port].Dir {
			return nil, fail(ErrInvalidConnection,
				fmt.Sprintf("%s <-> %s", link[0], link[1]),
				"链路必须连接一个输出端口与一个输入端口")
		}
		if a.dev.wireTo[a.port] != nil || b.dev.wireTo[b.port] != nil {
			return nil, fail(ErrDuplicateWire,
				fmt.Sprintf("%s <-> %s", link[0], link[1]),
				"端口已被其他链路占用，不允许重复接线或分叉")
		}
		a.dev.wireTo[a.port] = b
		b.dev.wireTo[b.port] = a
	}

	// 3) 登记外部输入端口。
	inputSeen := map[string]bool{}
	for _, name := range raw.Inputs {
		ep, err := c.resolveEndpoint(name, "inputs")
		if err != nil {
			return nil, err
		}
		if ep.dev.ports[ep.port].Dir != optics.PortDirIn {
			return nil, fail(ErrInvalidConnection, name, "外部输入只能连接器件输入端口")
		}
		if ep.dev.wireTo[ep.port] != nil || ep.dev.external[ep.port] {
			return nil, fail(ErrDuplicatePort, name, "外部端口重复声明或端口已接线")
		}
		if inputSeen[name] {
			return nil, fail(ErrDuplicatePort, name, "外部输入重复声明")
		}
		inputSeen[name] = true
		ep.dev.external[ep.port] = true
		c.inputs = append(c.inputs, ep)
		c.inputNames = append(c.inputNames, name)
	}

	// 4) 登记外部输出端口。
	outputSeen := map[string]bool{}
	for _, name := range raw.Outputs {
		ep, err := c.resolveEndpoint(name, "outputs")
		if err != nil {
			return nil, err
		}
		if ep.dev.ports[ep.port].Dir != optics.PortDirOut {
			return nil, fail(ErrInvalidConnection, name, "外部输出只能连接器件输出端口")
		}
		if ep.dev.wireTo[ep.port] != nil {
			return nil, fail(ErrDuplicatePort, name, "外部输出端口已接内部链路")
		}
		if outputSeen[name] {
			return nil, fail(ErrDuplicatePort, name, "外部输出重复声明")
		}
		outputSeen[name] = true
		ep.dev.external[ep.port] = true
		c.outputs = append(c.outputs, ep)
		c.outputNames = append(c.outputNames, name)
	}

	// 5) 悬空端口检查：每个端口必须是“接内部链路”或“外部输入/输出”二者之一。
	for _, cd := range c.devices {
		for pi, p := range cd.ports {
			if cd.wireTo[pi] == nil && !cd.external[pi] {
				return nil, fail(ErrDanglingPort, fmt.Sprintf("%s.%s", cd.id, p.Name),
					"端口悬空：既未接线也未声明为外部端口")
			}
		}
	}

	// 6) 拓扑排序：从外部输入驱动的器件出发。要求无环。
	if err := c.toposort(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Circuit) resolveEndpoint(s, where string) (*endpoint, error) {
	id, portName, ok := strings.Cut(strings.TrimSpace(s), ".")
	if !ok || id == "" || portName == "" {
		return nil, fail(ErrMalformedFile, where, "端点 %q 必须形如 deviceID.portName", s)
	}
	cd, ok := c.devices[id]
	if !ok {
		return nil, fail(ErrUnknownDevice, s, "引用了不存在的器件 %q", id)
	}
	for pi, p := range cd.ports {
		if p.Name == portName {
			return &endpoint{dev: cd, port: pi}, nil
		}
	}
	return nil, fail(ErrUnknownPort, s, "器件 %s 不存在端口 %q", id, portName)
}

func (c *Circuit) toposort() error {
	indeg := map[*compiledDevice]int{}
	adj := map[*compiledDevice][]*compiledDevice{}
	for _, cd := range c.devices {
		for pi, p := range cd.ports {
			if p.Dir != optics.PortDirIn {
				continue
			}
			if ep := cd.wireTo[pi]; ep != nil {
				indeg[cd]++
				adj[ep.dev] = append(adj[ep.dev], cd)
			}
		}
	}
	// 起点按固定（id）顺序入队，保证编译结果确定。
	var queue []*compiledDevice
	for _, raw := range c.sortedDeviceIDs() {
		cd := c.devices[raw]
		if indeg[cd] == 0 {
			queue = append(queue, cd)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cd := queue[0]
		queue = queue[1:]
		c.order = append(c.order, cd)
		visited++
		for _, next := range uniqueSorted(adj[cd]) {
			indeg[next]--
			if indeg[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(c.devices) {
		return fail(ErrCyclicNetlist, "", "网表中存在有向环路")
	}
	return nil
}

func (c *Circuit) sortedDeviceIDs() []string {
	ids := make([]string, 0, len(c.devices))
	for id := range c.devices {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}
