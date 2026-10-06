# 网表格式

网表为本地 JSON 文件，结构如下：

```json
{
  "name": "mzi",
  "devices": [
    {"id": "s1", "type": "splitter", "params": {"coupling_ratio": 0.5}}
  ],
  "links": [
    ["s1.out0", "top.in"]
  ],
  "inputs": ["s1.in0"],
  "outputs": ["s2.out0", "s2.out1"]
}
```

## 字段

- `name`：非空光路名称。
- `devices`：器件数组。`id` 必须非空且在本网表内唯一；`type` 必须是受支持类型；`params` 为数值参数表，只允许该器件模型声明过的参数（见 `docs/device-models.md`）。
- `links`：内部链路数组，每条链路是两个端点 `["deviceID.portName", "deviceID.portName"]`。
- `inputs`：外部输入端口列表，元素为端点字符串，只能引用器件的**输入**端口。
- `outputs`：外部输出端口列表，只能引用器件的**输出**端口。

## 端点与端口

端点统一写作 `器件id.端口名`。各器件端口名固定：

| 器件 | 输入端口 | 输出端口 |
| --- | --- | --- |
| `waveguide` | `in` | `out` |
| `phaseshifter` | `in` | `out` |
| `splitter` | `in0`, `in1` | `out0`（直通/bar）, `out1`（交叉/cross） |

## 接线规则（违反即拒绝）

1. 链路必须连接一个输出端口与一个输入端口；输出接输出、输入接输入、端口接自己都拒绝。
2. 每个端口至多接入一条链路；重复接线/分叉拒绝。
3. 外部输入/输出端口不能再接内部链路，也不能重复声明。
4. 每个端口必须恰好属于“接了内部链路”或“声明为外部端口”之一，否则按**悬空端口**拒绝。
5. 引用不存在的器件/端口分别按未知器件、未知端口拒绝。
6. 由链路构成的器件依赖图必须是有向无环图（从器件输出流向输入）；有环拒绝（仿真按固定拓扑序传播，不做迭代求解）。

## 拒绝原因码

编译失败返回 `*netlist.NetlistError`，含稳定原因码 `Reason` 与定位 `Where`：

| 原因码 | 含义 |
| --- | --- |
| `unknown_device` | 未知/不支持的器件类型，或链路引用了不存在的器件 |
| `unknown_parameter` | 器件带了模型未声明的参数 |
| `missing_parameter` | 必填参数缺失 |
| `invalid_parameter` | 参数越界或非有限值 |
| `unknown_port` | 引用了器件上不存在的端口 |
| `duplicate_device_id` | 器件 id 重复 |
| `duplicate_wire` | 端口被重复接线 |
| `invalid_connection` | 输入接输入/输出接输出、端口接自己、外部端口方向错误 |
| `dangling_port` | 端口悬空（未接线也不是外部端口） |
| `duplicate_external_port` | 外部端口重复声明或与内部链路冲突 |
| `cyclic_netlist` | 网表存在有向环路 |
| `malformed_file` | JSON 非法、缺 name、id 为空等结构问题 |

系统不会对任何错误做“按理想器件顶替”“静默忽略”或“替用户猜参数”。
