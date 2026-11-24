package licenser

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// readAudit 读出审计文件的全部记录。
func readAudit(t *testing.T, a *Auditor) []AuditEntry {
	t.Helper()
	if a == nil {
		t.Fatal("auditor is nil")
	}
	raw, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var out []AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad audit line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

// 导入成功要留痕，且记录里不能出现完整授权码。
//
// 授权码本身就是凭证，落进日志等于把凭证副本散落到各处；
// 只保留末 8 位用于对账即可。
func TestAuditAppliedHidesFullCode(t *testing.T) {
	pub, priv := testKeyPair(1)
	dir := t.TempDir()
	auditor := NewAuditor(dir)

	store := license.NewStore(license.StoreOptions{
		Dir:       t.TempDir(),
		PublicKey: pub,
		LocalMACs: []string{macA},
		Now:       func() time.Time { return time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC) },
	})
	code, err := license.Encode(license.Payload{
		V: 1, SN: "SN-AUDIT", FP: license.Fingerprint([]string{macA}),
		MAC:  []string{macA},
		IAT:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		EXP:  time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		Feat: []license.Feature{license.FeatureKB}, User: "李四", Org: "某所",
	}, priv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	svc := NewService(Options{Store: store, Enabled: true, Auditor: auditor})
	if _, err := svc.Apply(code); err != nil {
		t.Fatalf("apply: %v", err)
	}

	entries := readAudit(t, auditor)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	got := entries[0]
	if got.Event != AuditEventApplied || got.Serial != "SN-AUDIT" || got.User != "李四" || got.Org != "某所" {
		t.Errorf("unexpected entry: %+v", got)
	}
	// 末 8 位对得上，但完整码不得出现。
	if got.CodeTag != code[len(code)-8:] {
		t.Errorf("code tag = %q, want last 8 of code", got.CodeTag)
	}
	raw, _ := os.ReadFile(auditor.Path())
	if strings.Contains(string(raw), code) {
		t.Error("audit file must not contain the full license code")
	}
}

// 导入失败也要留痕，并带上真实原因——只记「被拒了」等于没记。
func TestAuditRejectedRecordsReason(t *testing.T) {
	pub, _ := testKeyPair(1)
	auditor := NewAuditor(t.TempDir())
	store := license.NewStore(license.StoreOptions{
		Dir:       t.TempDir(),
		PublicKey: pub,
		LocalMACs: []string{macA},
	})
	svc := NewService(Options{Store: store, Enabled: true, Auditor: auditor})

	if _, err := svc.Apply("WNL1.not-a-real-code.sig"); err == nil {
		t.Fatal("garbage code must fail")
	}

	entries := readAudit(t, auditor)
	if len(entries) != 1 || entries[0].Event != AuditEventRejected {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	if entries[0].Reason == "" {
		t.Error("rejected entry must carry a reason")
	}
}

// 状态跃迁要留痕。
func TestAuditTransitionOnExpiry(t *testing.T) {
	pub, priv := testKeyPair(1)
	auditor := NewAuditor(t.TempDir())
	exp := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	activated := exp.Add(-30 * 24 * time.Hour)

	store := license.NewStore(license.StoreOptions{
		Dir:       t.TempDir(),
		PublicKey: pub,
		LocalMACs: []string{macA},
		Now:       func() time.Time { return activated },
	})
	code, _ := license.Encode(license.Payload{
		V: 1, SN: "SN-TR", FP: license.Fingerprint([]string{macA}),
		MAC: []string{macA}, IAT: activated.Unix(), EXP: exp.Unix(),
		Feat: []license.Feature{license.FeatureKB},
	}, priv)
	if _, err := store.Apply(code); err != nil {
		t.Fatalf("apply: %v", err)
	}

	svc := NewService(Options{Store: store, Enabled: true, Auditor: auditor})
	store.SetNowFunc(func() time.Time { return exp.Add(60 * 24 * time.Hour) })
	svc.tick()

	var found bool
	for _, e := range readAudit(t, auditor) {
		if e.Event == AuditEventTransition && e.Prev == "valid" && e.State == "readonly" {
			found = true
		}
	}
	if !found {
		t.Error("must record valid->readonly transition")
	}
}

// 审计不可用时授权流程必须照常工作（磁盘满、只读挂载等场景）。
func TestAuditNilIsSafe(t *testing.T) {
	pub, _ := testKeyPair(1)
	store := license.NewStore(license.StoreOptions{
		Dir:       t.TempDir(),
		PublicKey: pub,
		LocalMACs: []string{macA},
	})
	svc := NewService(Options{Store: store, Enabled: true, Auditor: nil})
	// 只要不 panic 即可。
	if _, err := svc.Apply("garbage"); err == nil {
		t.Fatal("expected error")
	}
	if NewAuditor("") != nil {
		t.Error("empty dir must yield nil auditor")
	}
}
