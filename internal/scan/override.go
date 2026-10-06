package scan

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// rawNetlistJSON 是内部使用的网表结构（与 netlist.RawNetlist 的 JSON 形态一致），
// 这里独立声明以避免扫描包依赖网表的解析错误类型。
type rawNetlistJSON struct {
	Name    string          `json:"name"`
	Devices []rawDeviceJSON `json:"devices"`
	Links   [][2]string     `json:"links"`
	Inputs  []string        `json:"inputs"`
	Outputs []string        `json:"outputs"`
}

type rawDeviceJSON struct {
	ID     string             `json:"id"`
	Type   string             `json:"type"`
	Params map[string]float64 `json:"params"`
}

// applyOverrides 在不修改原始 JSON 的前提下，生成覆盖参数后的网表 JSON。
// 覆盖目标器件不存在时直接报错（不猜测、不静默忽略），参数合法性在 netlist.Parse 阶段统一拒绝。
func applyOverrides(raw []byte, ovs []Override) ([]byte, error) {
	var nl rawNetlistJSON
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&nl); err != nil {
		return nil, fmt.Errorf("扫描覆盖: 原始网表解析失败: %w", err)
	}
	index := map[string]int{}
	for i, d := range nl.Devices {
		index[d.ID] = i
	}
	// 同一扫描点内对同一参数的重复覆盖视为冲突，直接拒绝。
	seen := map[string]bool{}
	for _, o := range ovs {
		key := o.Device + "." + o.Param
		if seen[key] {
			return nil, fmt.Errorf("扫描覆盖: 参数 %s 在同一扫描点被重复覆盖", key)
		}
		seen[key] = true
		di, ok := index[o.Device]
		if !ok {
			return nil, fmt.Errorf("扫描覆盖: 器件 %q 不存在于网表", o.Device)
		}
		if nl.Devices[di].Params == nil {
			nl.Devices[di].Params = map[string]float64{}
		}
		nl.Devices[di].Params[o.Param] = o.Value
	}
	out, err := json.Marshal(&nl)
	if err != nil {
		return nil, fmt.Errorf("扫描覆盖: 重新序列化失败: %w", err)
	}
	return out, nil
}
