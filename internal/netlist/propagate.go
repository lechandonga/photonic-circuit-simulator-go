package netlist

import (
	"fmt"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/optics"
)

// Propagate 在不可变编译光路上执行一次相干传播。
// inputs 以 "deviceID.portName" 为键提供外部激励；返回外部输入/输出的名称与复振幅，顺序固定。
// 本函数不写 Circuit 的任何字段，仅使用函数内局部状态，可安全并发调用。
func (c *Circuit) Propagate(wavelengthNm float64, inputs map[string]optics.Field) (
	inNames []string, inFields []optics.Field, outNames []string, outFields []optics.Field, err error,
) {
	inputIndex := make(map[string]int, len(c.inputs))
	inFields = make([]optics.Field, len(c.inputs))
	inNames = append(inNames, c.inputNames...)
	for i, name := range c.inputNames {
		inputIndex[name] = i
	}
	for name, f := range inputs {
		idx, ok := inputIndex[name]
		if !ok {
			return nil, nil, nil, nil, fmt.Errorf("输入端口 %q 未在网表 inputs 中声明", name)
		}
		inFields[idx] = f
	}

	// values[device][port]：当前已到达该端口的场。只有输入端口槽位会被驱动。
	values := make(map[*compiledDevice][]optics.Field, len(c.order))
	for _, cd := range c.order {
		values[cd] = make([]optics.Field, len(cd.ports))
	}
	// 注入外部输入。
	for i, ep := range c.inputs {
		values[ep.dev][ep.port] = inFields[i]
	}
	// 按拓扑序逐器件散射；输出立即写入对端输入槽。
	for _, cd := range c.order {
		ins := make([]optics.Field, 0, len(cd.ports))
		outSlots := make([]int, 0, len(cd.ports))
		for pi, p := range cd.ports {
			if p.Dir == optics.PortDirIn {
				ins = append(ins, values[cd][pi])
			} else {
				outSlots = append(outSlots, pi)
			}
		}
		outs := cd.device.Apply(ins, wavelengthNm)
		if len(outs) != len(outSlots) {
			return nil, nil, nil, nil,
				fmt.Errorf("内部错误: 器件 %s 输出数 %d 与端口表 %d 不一致", cd.id, len(outs), len(outSlots))
		}
		for oi, pi := range outSlots {
			f := outs[oi]
			values[cd][pi] = f
			if ep := cd.wireTo[pi]; ep != nil {
				values[ep.dev][ep.port] = f
			}
		}
	}

	outNames = append(outNames, c.outputNames...)
	outFields = make([]optics.Field, len(c.outputs))
	for i, ep := range c.outputs {
		outFields[i] = values[ep.dev][ep.port]
	}
	return inNames, inFields, outNames, outFields, nil
}
