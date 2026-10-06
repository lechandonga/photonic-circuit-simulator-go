package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lechandonga/photonic-circuit-simulator-go/internal/engine"
	"github.com/lechandonga/photonic-circuit-simulator-go/internal/optics"
)

// multiInput 解析重复出现的 --input deviceID.port=power[:phase]。
type multiInput struct {
	ports []engine.InputPort
}

func (m *multiInput) String() string { return fmt.Sprint(m.ports) }

func (m *multiInput) Set(v string) error {
	port, spec, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("--input %q 必须形如 deviceID.port=功率[:相位]", v)
	}
	powerStr, phaseStr, hasPhase := strings.Cut(spec, ":")
	power, err := strconv.ParseFloat(powerStr, 64)
	if err != nil || power < 0 {
		return fmt.Errorf("--input %q 的功率非法", v)
	}
	phase := 0.0
	if hasPhase {
		phase, err = strconv.ParseFloat(phaseStr, 64)
		if err != nil {
			return fmt.Errorf("--input %q 的相位非法", v)
		}
	}
	m.ports = append(m.ports, engine.InputPort{
		Name: strings.TrimSpace(port), Field: optics.NewField(power, phase),
	})
	return nil
}
