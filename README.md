# photonic-circuit-simulator-go

纯本地、离线运行的光子芯片光路仿真组件（Go，仅标准库）。不连接真实设备、不访问任何外部服务、无需要联网获取的依赖。

## 能力概览

- 根据本地 JSON 网表构建光路，做相干复振幅传播仿真；覆盖**波导 waveguide、分束器 splitter、移相器 phaseshifter**。
- 输出各外部端口的**功率**与**相对相位**；理想无损耗光路总能量守恒，默认容差 `1e-12`。
- 支持插入损耗（dB）、相位偏置（弧度）等非理想参数，作用规则固定可解释（见 `docs/device-models.md`）。
- 网表中的未知器件/参数、端口不匹配、重复接线、输入接输入、悬空端口、参数越界、有环网表等一律**明确拒绝**，返回带稳定原因码的错误，绝不按理想器件顶替或静默猜测。
- 同一光路可批量扫描多组参数/波长，扫描**并发执行**；各扫描点使用独立编译结果（或按规范化 key 命中的独立缓存项），不串结果；单点失败被隔离并可定位到具体扫描点；结果按索引排序，重复运行逐点一致；支持中途取消且已产出结果自洽。

## 目录结构

| 路径 | 职责 |
| --- | --- |
| `internal/optics` | 相干复振幅场、器件散射矩阵与参数规则 |
| `internal/netlist` | 网表 JSON 结构、校验拒绝、拓扑排序与传播 |
| `internal/engine` | 单次仿真执行、端口结果、能量守恒校验 |
| `internal/scan` | 批量并发扫描、参数覆盖、共享缓存、取消 |
| `cmd/pcsim` | 本地命令行：`run` 单点仿真、`scan` 批量扫描 |
| `examples/` | 理想/含损耗 MZI 网表与相位扫描定义示例 |
| `docs/` | 网表格式、器件模型、容差与验证方法文档 |

## 快速开始

需要 Go 1.25+（仅标准库）。

```bash
# 单点仿真：向 MZI 的 s1.in0 注入单位功率
go run ./cmd/pcsim run --net examples/mzi_ideal.json --wavelength-nm 1550 --input s1.in0=1.0

# 含损耗光路
go run ./cmd/pcsim run --net examples/mzi_lossy.json --wavelength-nm 1550 --input split1.in0=1.0

# 并发批量相位扫描
go run ./cmd/pcsim scan --net examples/mzi_scan_base.json --scan examples/mzi_phase_scan.json
```

`--input` 形如 `deviceID.port=功率[:相位弧度]`，可重复指定多个外部输入。

## 本地验证

```bash
# 竞态检测 + 详细业务日志
CGO_ENABLED=1 go test -race -v ./...

# 常规测试
go test ./...
```

测试按业务组织（理想/解析解、含损耗、非法网表原因码、并发扫描隔离/取消/共享缓存），
`-v` 日志中每个用例会打印“业务[名称] …”说明覆盖了什么、结果是否与解析解/守恒一致。

## 文档

- 网表格式：`docs/netlist-format.md`
- 器件模型（含损耗与相位偏差约定）：`docs/device-models.md`
- 容差判定与确定性/并发语义：`docs/semantics.md`
