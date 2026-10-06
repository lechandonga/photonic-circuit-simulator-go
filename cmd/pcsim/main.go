// pcsim 是纯本地的光子芯片光路仿真命令行工具：
// 编译并运行单个网表，或从 JSON 扫描文件批量并发执行。不访问网络与真实硬件。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/engine"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/netlist"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/optics"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/scan"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pcsim:", err)
		os.Exit(1)
	}
}

type cliInput struct {
	Port  string  `json:"port"`
	Power float64 `json:"power"`
	Phase float64 `json:"phase_rad"`
}

type scanFile struct {
	Workers int `json:"workers"`
	Points  []struct {
		Name         string             `json:"name"`
		WavelengthNm float64            `json:"wavelength_nm"`
		Overrides    map[string]float64 `json:"overrides"`
		Inputs       []cliInput         `json:"inputs"`
	} `json:"points"`
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("缺少子命令")
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "scan":
		return cmdScan(args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("未知子命令 %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `pcsim - 本地光子芯片光路仿真器(离线, 仅标准库)

用法:
  pcsim run  --net <网表.json> --wavelength-nm 1550 --input split1.in0=1.0
  pcsim scan --net <网表.json> --scan <扫描定义.json>

run:  编译并仿真单个网表，JSON 输出各端口功率与相对相位。
scan: 按扫描定义并发执行多组参数/波长覆盖，逐点输出结果与失败原因。
`)
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	netPath := fs.String("net", "", "网表 JSON 路径")
	wl := fs.Float64("wavelength-nm", 1550, "波长(纳米)")
	var inputs multiInput
	fs.Var(&inputs, "input", "外部输入，形如 deviceID.port=功率(:相位弧度)，可重复")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *netPath == "" {
		return fmt.Errorf("--net 为必填")
	}
	c, err := netlist.LoadFile(*netPath)
	if err != nil {
		return err
	}
	res, err := engine.Run(c, engine.RunOptions{WavelengthNm: *wl, Inputs: inputs.ports})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(encodeResult(res))
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	netPath := fs.String("net", "", "基础网表 JSON 路径")
	scanPath := fs.String("scan", "", "扫描定义 JSON 路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *netPath == "" || *scanPath == "" {
		return fmt.Errorf("--net 与 --scan 均为必填")
	}
	raw, err := os.ReadFile(*netPath)
	if err != nil {
		return err
	}
	var sf scanFile
	if err := json.Unmarshal(mustReadFile(*scanPath), &sf); err != nil {
		return fmt.Errorf("扫描文件解析失败: %w", err)
	}
	points := make([]scan.Point, 0, len(sf.Points))
	for _, p := range sf.Points {
		ovs := make([]scan.Override, 0, len(p.Overrides))
		for k, v := range p.Overrides {
			dev, param, ok := splitKey(k)
			if !ok {
				return fmt.Errorf("覆盖键 %q 必须形如 deviceID.paramName", k)
			}
			ovs = append(ovs, scan.Override{Device: dev, Param: param, Value: v})
		}
		in := make([]engine.InputPort, 0, len(p.Inputs))
		for _, ci := range p.Inputs {
			in = append(in, engine.InputPort{Name: ci.Port, Field: optics.NewField(ci.Power, ci.Phase)})
		}
		points = append(points, scan.Point{
			Name: p.Name, WavelengthNm: p.WavelengthNm, Overrides: ovs, Inputs: in,
		})
	}
	runner := scan.NewRunner(raw, scan.NewCache(), sf.Workers)
	out, err := runner.Run(context.Background(), points)
	if err != nil {
		return err
	}
	type pointOut struct {
		Index  int             `json:"index"`
		Name   string          `json:"name"`
		Error  string          `json:"error,omitempty"`
		Result json.RawMessage `json:"result,omitempty"`
	}
	enc := json.NewEncoder(os.Stdout)
	for _, pr := range out.Points {
		po := pointOut{Index: pr.Index, Name: pr.Name}
		if pr.Error != nil {
			po.Error = pr.Error.Error()
		} else {
			b, _ := json.Marshal(encodeResult(pr.Result))
			po.Result = b
		}
		if err := enc.Encode(po); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "完成 %d/%d 个扫描点\n", out.Completed, len(out.Points))
	return nil
}

func mustReadFile(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		panic(err)
	}
	return b
}

func splitKey(k string) (string, string, bool) {
	for i := len(k) - 1; i >= 0; i-- {
		if k[i] == '.' {
			return k[:i], k[i+1:], i > 0 && i+1 < len(k)
		}
	}
	return "", "", false
}

type resultJSON struct {
	WavelengthNm float64    `json:"wavelength_nm"`
	InputPower   float64    `json:"input_power"`
	OutputPower  float64    `json:"output_power"`
	Outputs      []portJSON `json:"outputs"`
}

type portJSON struct {
	Port       string  `json:"port"`
	Power      float64 `json:"power"`
	PhaseRad   float64 `json:"phase_rad"`
	PhaseValid bool    `json:"phase_valid"`
}

func encodeResult(r *engine.Result) resultJSON {
	out := resultJSON{WavelengthNm: r.WavelengthNm, InputPower: r.InputPower, OutputPower: r.OutputPower}
	for _, p := range r.Outputs {
		out.Outputs = append(out.Outputs, portJSON{
			Port: p.Name, Power: p.Power, PhaseRad: p.Phase, PhaseValid: p.PhaseValid,
		})
	}
	return out
}
