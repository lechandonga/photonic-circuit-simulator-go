package netlist

import "fmt"

// Reason 是网表被拒绝的可区分原因码。
type Reason string

const (
	ErrUnknownDevice     Reason = "unknown_device"          // 出现未支持的器件类型
	ErrUnknownParameter  Reason = "unknown_parameter"       // 器件带了模型之外的参数
	ErrMissingParameter  Reason = "missing_parameter"       // 必填参数缺失
	ErrInvalidParameter  Reason = "invalid_parameter"       // 参数越界或非有限值
	ErrUnknownPort       Reason = "unknown_port"            // 引用了器件上不存在的端口
	ErrPortCountMismatch Reason = "port_count_mismatch"     // 端口表与器件模型不一致
	ErrDuplicateID       Reason = "duplicate_device_id"     // 器件 id 重复
	ErrDuplicateWire     Reason = "duplicate_wire"          // 同一端口被重复接线
	ErrInvalidConnection Reason = "invalid_connection"      // 输入接输入/输出接输出等非法接线
	ErrDanglingPort      Reason = "dangling_port"           // 端口既未接线也未声明为外部端口
	ErrDuplicatePort     Reason = "duplicate_external_port" // 外部端口重复声明
	ErrCyclicNetlist     Reason = "cyclic_netlist"          // 网表存在环路，无法按拓扑序传播
	ErrMalformedFile     Reason = "malformed_file"          // JSON 结构或字段非法
)

// NetlistError 携带稳定的拒绝原因码与定位信息。
type NetlistError struct {
	Reason Reason
	Where  string // 器件 id / 接线端点等定位信息
	Msg    string
}

func (e *NetlistError) Error() string {
	if e.Where != "" {
		return fmt.Sprintf("网表错误[%s] @%s: %s", e.Reason, e.Where, e.Msg)
	}
	return fmt.Sprintf("网表错误[%s]: %s", e.Reason, e.Msg)
}

func fail(reason Reason, where, format string, args ...any) error {
	return &NetlistError{Reason: reason, Where: where, Msg: fmt.Sprintf(format, args...)}
}
