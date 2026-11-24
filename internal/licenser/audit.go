package licenser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// 审计事件类型。
//
// 命名用「名词_动词」而不是「动词_名词」：按前缀筛选时同类事件会
// 聚在一起（如所有 license_* 一起），grep 与日志平台都更好用。
const (
	AuditEventApplied    = "license_applied"    // 导入成功
	AuditEventRejected   = "license_rejected"   // 导入失败
	AuditEventTransition = "license_transition" // 状态跃迁
	// AuditEventHorizonReset 记录「历史到期水位被清空」。
	//
	// 单独成一类事件，是因为它降低了这台机器的防护强度：运维事后翻
	// 审计要能立刻看到「某天某张码把水位从 A 降到了 B」，以及为什么。
	AuditEventHorizonReset = "license_horizon_reset"
)

// AuditEntry 是一条审计记录。
//
// 不含完整授权码：码本身就是凭证，写入磁盘日志等于把凭证次级副本
// 散落在各处。只保留序列号与末 8 位，足够对账，不足以复用到别的机器。
type AuditEntry struct {
	At      int64  `json:"at"`
	Event   string `json:"event"`
	State   string `json:"state"`
	Prev    string `json:"prev,omitempty"`
	Serial  string `json:"serial,omitempty"`
	User    string `json:"user,omitempty"`
	Org     string `json:"org,omitempty"`
	FP      string `json:"fp,omitempty"`
	Reason  string `json:"reason,omitempty"`
	CodeTag string `json:"code_tag,omitempty"` // 授权码末 8 位
}

// Auditor 把审计记录追加写入 JSONL 文件。
//
// 为什么单独一个文件而不是混在业务日志里：业务日志按天轮转、
// 量大且会被清理；授权审计是低频（一年几次）且必须长期可查的记录，
// 混进去后找一条授权记录要翻几十万行。
type Auditor struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

// NewAuditor 在 dir 下准备审计文件。
//
// 返回 nil 表示审计不可用（目录创建失败），调用方需要判空——
// 审计失败绝不能阻断授权本身，否则用户会因为日志盘满而进不去系统。
func NewAuditor(dir string) *Auditor {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	return &Auditor{
		path: filepath.Join(dir, "license-audit.jsonl"),
		now:  time.Now,
	}
}

// Path 返回审计文件路径，便于排查时提示用户。
func (a *Auditor) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// Record 追加一条记录。
//
// 错误被吞掉并返回给调用方判空即可：审计写不进去（磁盘满、只读挂载）
// 时授权流程必须照常进行，宁可少一条审计也不能让用户进不去。
func (a *Auditor) Record(entry AuditEntry) error {
	if a == nil {
		return nil
	}
	if entry.At == 0 {
		entry.At = a.now().Unix()
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// codeTag 取授权码的末 8 位，用于在审计里指认「哪一张码"。
//
// 取末 8 位而不是前 8 位：格式固定为 WNL1.<payload>.<sig>，前 8 位
// 对所有人都一样，起不到区分作用。
func codeTag(code string) string {
	if len(code) <= 8 {
		return code
	}
	return code[len(code)-8:]
}

// auditApplied 记录一次成功的导入。
func auditApplied(a *Auditor, ev license.Evaluation, code string) {
	if a == nil || ev.Payload == nil {
		return
	}
	_ = a.Record(AuditEntry{
		Event:   AuditEventApplied,
		State:   string(ev.State),
		Serial:  ev.Payload.SN,
		User:    ev.Payload.User,
		Org:     ev.Payload.Org,
		FP:      ev.Payload.FP,
		CodeTag: codeTag(code),
	})
}

// auditRejected 记录一次失败的导入。
//
// 失败原因原样记录：审计的价值恰恰在于「为什么被拒」，只记「被拒了」
// 等于没记。
func auditRejected(a *Auditor, reason, code string) {
	if a == nil {
		return
	}
	_ = a.Record(AuditEntry{
		Event:   AuditEventRejected,
		State:   string(license.StateInvalid),
		Reason:  reason,
		CodeTag: codeTag(code),
	})
}

// auditHorizonReset 记录一次水位清空。
//
// 只在水位真的被丢掉时调用：导入一张更晚到期的码也会刷新水位上限，
// 但那不降低防护强度，混在一起会让这条事件失去筛选价值。
func auditHorizonReset(a *Auditor, outcome license.ApplyOutcome, ev license.Evaluation, code string) {
	if a == nil || outcome.HorizonFrom == 0 {
		return
	}
	reason := "旧授权码在当前公钥下已无法验签，历史水位作废"
	if outcome.Reason == license.ResetReasonIssuer {
		reason = "导入复位码，发码方主动清空历史水位"
	}
	entry := AuditEntry{
		Event:   AuditEventHorizonReset,
		State:   string(ev.State),
		Reason:  fmt.Sprintf("%s（原水位至 %s，新码至 %s）", reason,
			time.Unix(outcome.HorizonFrom, 0).Format("2006-01-02"),
			formatExpiry(ev.ExpiresAt)),
		CodeTag: codeTag(code),
	}
	if ev.Payload != nil {
		entry.Serial = ev.Payload.SN
		entry.User = ev.Payload.User
		entry.Org = ev.Payload.Org
		entry.FP = ev.Payload.FP
	}
	_ = a.Record(entry)
}

// formatExpiry 把到期时间格式化成日期，永久授权返回「永久」。
func formatExpiry(exp int64) string {
	if exp == 0 {
		return "永久"
	}
	return time.Unix(exp, 0).Format("2006-01-02")
}

// auditTransition 记录一次状态跃迁。
func auditTransition(a *Auditor, prev, next license.Evaluation) {
	if a == nil {
		return
	}
	entry := AuditEntry{
		Event:  AuditEventTransition,
		State:  string(next.State),
		Prev:   string(prev.State),
		Reason: next.Reason,
	}
	if next.Payload != nil {
		entry.Serial = next.Payload.SN
		entry.User = next.Payload.User
		entry.Org = next.Payload.Org
		entry.FP = next.Payload.FP
	}
	_ = a.Record(entry)
}

// describeAudit 返回一句便于写日志的摘要。
func describeAudit(entry AuditEntry) string {
	if entry.Prev != "" {
		return fmt.Sprintf("%s %s->%s serial=%s", entry.Event, entry.Prev, entry.State, entry.Serial)
	}
	return fmt.Sprintf("%s state=%s serial=%s", entry.Event, entry.State, entry.Serial)
}
