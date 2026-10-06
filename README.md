# photonic-circuit-simulator-go

本地离线运行的光子芯片光路仿真组件。根据本地 JSON 网表构建光路，做相干光传播仿真，
输出各端口的功率与相对相位。仅依赖 Go 标准库，不连接真实设备或外部服务。

## 网表格式

网表为本地 JSON 文件（示例见 `testdata/mzi.json`，一个马赫-曾德干涉仪）：

```json
{
  "name": "mach-zehnder",
  "inputs": ["in"],
  "outputs": ["dump", "out0", "out1"],
  "devices": [
    {"id": "bs1", "type": "beamsplitter", "params": {"split_ratio": 0.5}},
    {"id": "ps",  "type": "phaseshifter", "params": {"phase_deg": 90}}
  ],
  "connections": [
    {"from": {"device": "$input", "port": "in"}, "to": {"device": "bs1", "port": "in1"}}
  ]
}
```

- 外部端口用保留器件名 `$input` / `$output` 引用，端口名为 `inputs`/`outputs` 中声明的名字。
- 每条 `connection` 连接两个端点；每个端点必须恰好被连接一次。
- 仿真时每个输入端口注入单位振幅、零相位的相干光；输出端口吸收。

## 器件模型

所有器件为线性散射器件，端口名固定：

| 类型 | 端口 | 参数（缺省值） |
|---|---|---|
| `waveguide` | `in`, `out` | `length_um`(0), `neff`(0), `phase_deg`(0), `insertion_loss_db`(0), `phase_bias_deg`(0) |
| `phaseshifter` | `in`, `out` | `phase_deg`(0), `insertion_loss_db`(0), `phase_bias_deg`(0) |
| `beamsplitter` | `in1`, `in2`, `out1`, `out2` | `split_ratio`(0.5), `insertion_loss_db`(0), `phase_bias_deg`(0) |

非理想参数约定（固定且可解释）：

- **插入损耗** `insertion_loss_db`：振幅乘 `g = 10^(-IL/20)`，即功率乘 `10^(-IL/10)`。范围 `[0, 60]`。
- **相位偏置** `phase_bias_deg`：与器件理想相位**等效叠加**（波导/移相器加到传输相位上；
  分束器整体乘 `exp(i·bias)`）。范围 `[-3600, 3600]`。
- **波导物理相位**：`2π·neff·length_um·1000 / wavelength_nm`，与 `phase_deg`、`phase_bias_deg` 相加。
- **分束器**：`t = sqrt(1-split_ratio)`，`r = sqrt(split_ratio)`，
  散射子矩阵（in→out）为 `g·e^{i·bias}·[[t, i·r], [i·r, t]]`，器件互易、无损耗时酉。

## 求解与容差

- 对每个器件端口建立散射方程并联立成线性方程组，用带部分主元（列内最大模，规则固定）
  的高斯消元求解。无随机性、无迭代，结果确定且可复现。
- **能量守恒容差**：无损耗光路要求 `|ΣP_out − ΣP_in| ≤ 1e-9`（`sim.EnergyTolerance`，
  可通过 `Engine.EnergyTol` 调整）。超差返回 `sim.ErrEnergyNotConserved`。
- 理想 MZI 解析解：`P(out0) = sin²(φ/2)`，`P(out1) = cos²(φ/2)`，测试以 `1e-12` 精度比对。

## 网表校验

解析即严格校验，非法网表整体拒绝，绝不按理想器件顶上或静默忽略。拒绝原因可区分
（`errors.Is` 可判定）：

| 错误码 | 含义 |
|---|---|
| `netlist.ErrUnknownDevice` | 未知器件类型 |
| `netlist.ErrPortMismatch` | 端点引用了不存在的器件/端口 |
| `netlist.ErrDuplicateConnection` | 同一端点被重复接线（含自连） |
| `netlist.ErrDanglingPort` | 存在悬空端口（列出全部） |
| `netlist.ErrParamOutOfRange` | 参数值越界 |
| `netlist.ErrUnknownParam` | 参数名不存在（含扫描覆盖） |
| `netlist.ErrDuplicateID` | 器件 ID 重复 |

## 批量扫描

`sweep.Run(ctx, engine, request)` 并发执行多参数/多波长扫描：

- 每个扫描点在独立网表副本上仿真，任务间不共享可变状态，结果按索引写回固定槽位，
  重复执行逐点一致；
- 单点失败（如覆盖参数越界）只记录在该点的 `PointResult.Err`，不影响其它点，
  失败点可通过 `Index`/`Label` 定位；
- `ctx` 取消后停止派发新点，已产出的结果保持完整自洽。

## 本地验证

```bash
go test ./...                    # 运行全部单测
go test -v ./...                 # 查看各业务覆盖与用例明细
CGO_ENABLED=1 go test -race ./...  # 开启竞态检测
```

测试覆盖：理想 MZI/分束器与解析解比对、含损耗与相位偏置光路、七类非法网表、
并发扫描确定性与单点失败隔离、取消自洽性、多波长扫描。
