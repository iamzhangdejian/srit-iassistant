package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 测试用的固定密钥。用固定种子生成，好处是失败时可以用同一份种子
// 复现；生产密钥绝不会来自这里。
var (
	testPub, testPriv = func() (ed25519.PublicKey, ed25519.PrivateKey) {
		seed := make([]byte, ed25519.SeedSize)
		for i := range seed {
			seed[i] = byte(i + 1)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		return priv.Public().(ed25519.PublicKey), priv
	}()
	// otherPriv 模拟「攻击者自己的密钥对」。
	_, otherPriv = func() (ed25519.PublicKey, ed25519.PrivateKey) {
		seed := make([]byte, ed25519.SeedSize)
		for i := range seed {
			seed[i] = byte(255 - i)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		return priv.Public().(ed25519.PublicKey), priv
	}()
)

// 测试用的 MAC 集合。
const (
	macWiFi  = "3c:22:fb:dd:ee:01"
	macWired = "f0:18:98:dd:ee:02"
	macOther = "11:22:33:44:55:66"
)

var localMACs = []string{macWiFi, macWired}

// mustEncode 用测试私钥签一张码，失败直接 Fatal。
func mustEncode(t *testing.T, p Payload) string {
	t.Helper()
	code, err := Encode(p, testPriv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return code
}

// basePayload 返回一张「签给本机、30 天后到期」的合法载荷。
func basePayload(now time.Time) Payload {
	return Payload{
		V:    1,
		SN:   "SN-2026-0001",
		FP:   Fingerprint(localMACs),
		MAC:  NormalizeMACs(localMACs),
		IAT:  now.Unix(),
		EXP:  now.Add(30 * 24 * time.Hour).Unix(),
		Feat: []Feature{FeatureKB, FeatureChat, FeatureOCR, FeatureOffice},
		Lim:  Limits{KB: 20, Seat: 1},
		Org:  "测试单位",
		User: "张三",
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := basePayload(now)
	code := mustEncode(t, p)

	got, err := Decode(code, testPub)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Payload.SN != p.SN {
		t.Errorf("SN = %q, want %q", got.Payload.SN, p.SN)
	}
	if got.Payload.EXP != p.EXP {
		t.Errorf("EXP = %d, want %d", got.Payload.EXP, p.EXP)
	}
	if !got.Payload.HasFeature(FeatureOffice) {
		t.Error("expected office feature to be granted")
	}
}

// 授权码要经人工复制粘贴，中间的换行与空格必须被容忍。
func TestDecodeToleratesWhitespace(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	code := mustEncode(t, basePayload(now))

	dirty := "  \n" + code[:10] + "\n " + code[10:30] + "\r\n" + code[30:] + "  \n"
	if _, err := Decode(dirty, testPub); err != nil {
		t.Fatalf("decode with whitespace: %v", err)
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	code := mustEncode(t, basePayload(now))
	parts := strings.Split(code, ".")

	tests := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "   ", ErrEmptyCode},
		{"two segments", parts[0] + "." + parts[1], ErrBadFormat},
		{"four segments", code + ".extra", ErrBadFormat},
		{"wrong prefix", "WNL9." + parts[1] + "." + parts[2], ErrBadFormat},
		{"payload not base64", parts[0] + ".!!!." + parts[2], ErrBadEncoding},
		{"signature not base64", parts[0] + "." + parts[1] + ".!!!", ErrBadEncoding},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.in, testPub)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// 篡改载荷后签名必须失效——这是整套授权机制的核心保证。
func TestDecodeRejectsTamperedPayload(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := basePayload(now)
	code := mustEncode(t, p)
	parts := strings.Split(code, ".")

	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	var tampered map[string]any
	if err := json.Unmarshal(body, &tampered); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// 把到期时间延后十年——攻击者最想改的就是这一个字段。
	tampered["exp"] = now.AddDate(10, 0, 0).Unix()
	newBody, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	forged := parts[0] + "." +
		base64.RawURLEncoding.EncodeToString(newBody) + "." + parts[2]

	if _, err := Decode(forged, testPub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// 用别人的私钥签出来的码，在本机的公钥下必须验不过。
func TestDecodeRejectsForeignKey(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	code, err := Encode(basePayload(now), otherPriv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := Decode(code, testPub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

// 签名有效但内容不自洽的码必须被拦住，错误要指向 ErrBadPayload。
func TestPayloadValidate(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	good := basePayload(now)

	tests := []struct {
		name   string
		mutate func(*Payload)
	}{
		{"missing serial", func(p *Payload) { p.SN = "" }},
		{"missing fingerprint", func(p *Payload) { p.FP = "" }},
		{"missing iat", func(p *Payload) { p.IAT = 0 }},
		{"exp before iat", func(p *Payload) { p.EXP = p.IAT - 10 }},
		{"unknown feature", func(p *Payload) { p.Feat = []Feature{"telepathy"} }},
		{"bad version", func(p *Payload) { p.V = 99 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := good
			tc.mutate(&p)
			code := mustEncode(t, p)
			if _, err := Decode(code, testPub); !errors.Is(err, ErrBadPayload) {
				t.Fatalf("err = %v, want ErrBadPayload", err)
			}
		})
	}
}

func TestFingerprintStableAcrossOrder(t *testing.T) {
	a := Fingerprint([]string{macWiFi, macWired})
	b := Fingerprint([]string{macWired, macWiFi})
	if a != b {
		t.Fatalf("fingerprint depends on order: %q vs %q", a, b)
	}
	if len(a) != FingerprintLen {
		t.Fatalf("fingerprint length = %d, want %d", len(a), FingerprintLen)
	}
}

func TestFingerprintNormalizesFormat(t *testing.T) {
	// 冒号、连字符、裸十六进制三种写法必须归一到同一个指纹。
	forms := [][]string{
		{"AA:BB:CC:DD:EE:01"},
		{"aa-bb-cc-dd-ee-01"},
		{"AABBCCDDEE01"},
	}
	want := Fingerprint(forms[0])
	for _, f := range forms {
		if got := Fingerprint(f); got != want {
			t.Errorf("Fingerprint(%v) = %q, want %q", f, got, want)
		}
	}
}

func TestNormalizeMACsDedupesAndSorts(t *testing.T) {
	// 用大写重复项验证「去重」：macWiFi 的十六进制大写形式必须归一到同一个值。
	got := NormalizeMACs([]string{macWired, macWiFi, strings.ToUpper(macWiFi), "garbage"})
	want := []string{macWiFi, macWired}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// 插网线、开热点都会改变网卡数量，但只要有一块网卡匹配就必须放行。
func TestMatchesIntersection(t *testing.T) {
	licenseMACs := []string{macWiFi, macWired}

	if !Matches(licenseMACs, []string{macWiFi}) {
		t.Error("expected match with single overlapping MAC")
	}
	if !Matches(licenseMACs, []string{macWiFi, macWired, macOther}) {
		t.Error("expected match when extra MACs are present")
	}
	if Matches(licenseMACs, []string{macOther}) {
		t.Error("expected no match for disjoint MAC sets")
	}
	if Matches(licenseMACs, nil) {
		t.Error("expected no match for empty local set")
	}
	if Matches(nil, localMACs) {
		t.Error("expected no match for empty license set")
	}
}

func TestIsUsableMACFiltersRandomized(t *testing.T) {
	tests := []struct {
		mac  string
		want bool
	}{
		{"3c:22:fb:11:22:33", true},  // 真实厂商前缀（Apple）
		{"f0:18:98:aa:bb:cc", true},  // 真实厂商前缀
		{"02:bb:cc:dd:ee:01", true},  // 本地管理：随机 MAC 必须接受
		{"aa:bb:cc:dd:ee:01", true},  // 本地管理：0xaa 的次低位为 1
		{"06:bb:cc:dd:ee:01", true},  // 本地管理
		{"00:00:00:00:00:00", false}, // 全零
		{"ff:ff:ff:ff:ff:ff", false}, // 广播
		{"01:00:5e:00:00:01", false}, // 组播
		{"3c:22:fb:11:22", false},    // 长度不足
	}
	for _, tc := range tests {
		if got := IsUsableMAC(tc.mac); got != tc.want {
			t.Errorf("IsUsableMAC(%q) = %v, want %v", tc.mac, got, tc.want)
		}
	}
}

func TestEvaluateStates(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	exp := now.Add(30 * 24 * time.Hour)

	newRecord := func() *Record {
		p := basePayload(now)
		p.EXP = exp.Unix()
		code := mustEncode(t, p)
		return &Record{Code: code, AppliedAt: now.Unix(), MaxExpSeen: p.EXP, LastSeenAt: now.Unix()}
	}

	t.Run("missing", func(t *testing.T) {
		ev := Evaluate(nil, testPub, localMACs, now)
		if ev.State != StateMissing {
			t.Fatalf("state = %q, want %q", ev.State, StateMissing)
		}
		if ev.CurrentFingerprint == "" {
			t.Error("expected fingerprint to be reported even when missing")
		}
	})

	t.Run("valid", func(t *testing.T) {
		ev := Evaluate(newRecord(), testPub, localMACs, now.Add(24*time.Hour))
		if ev.State != StateValid {
			t.Fatalf("state = %q, want valid (%s)", ev.State, ev.Reason)
		}
		if len(ev.Features) != 4 {
			t.Errorf("features = %v, want all 4", ev.Features)
		}
		if ev.RemainingDays != 29 {
			t.Errorf("remaining = %d, want 29", ev.RemainingDays)
		}
	})

	t.Run("grace", func(t *testing.T) {
		ev := Evaluate(newRecord(), testPub, localMACs, exp.Add(3*24*time.Hour))
		if ev.State != StateGrace {
			t.Fatalf("state = %q, want grace (%s)", ev.State, ev.Reason)
		}
		// 宽限期仍保留全部功能。
		if len(ev.Features) != 4 {
			t.Errorf("features = %v, want all 4 during grace", ev.Features)
		}
	})

	t.Run("readonly", func(t *testing.T) {
		ev := Evaluate(newRecord(), testPub, localMACs, exp.Add(GracePeriod+24*time.Hour))
		if ev.State != StateReadOnly {
			t.Fatalf("state = %q, want readonly (%s)", ev.State, ev.Reason)
		}
		// 只读期必须保留 kb 与 chat，砍掉 ocr 与 office。
		if len(ev.Features) != 2 {
			t.Fatalf("features = %v, want kb+chat only", ev.Features)
		}
		for _, f := range ev.Features {
			if f == FeatureOCR || f == FeatureOffice {
				t.Errorf("readonly should not grant %q", f)
			}
		}
	})

	t.Run("permanent ignores time", func(t *testing.T) {
		p := basePayload(now)
		p.EXP = 0
		code := mustEncode(t, p)
		rec := &Record{Code: code, AppliedAt: now.Unix(), LastSeenAt: now.Unix()}
		ev := Evaluate(rec, testPub, localMACs, now.AddDate(50, 0, 0))
		if ev.State != StateValid {
			t.Fatalf("state = %q, want valid for permanent license (%s)", ev.State, ev.Reason)
		}
	})

	t.Run("wrong machine", func(t *testing.T) {
		ev := Evaluate(newRecord(), testPub, []string{macOther}, now)
		if ev.State != StateInvalid {
			t.Fatalf("state = %q, want invalid (%s)", ev.State, ev.Reason)
		}
		if !strings.Contains(ev.Reason, "机器") {
			t.Errorf("reason %q should mention machine mismatch", ev.Reason)
		}
	})

	t.Run("empty fingerprint when machine unknown", func(t *testing.T) {
		p := basePayload(now)
		// 载荷里既没有 MAC 也没有指纹，必须判无效而不是放行。
		p.MAC = nil
		p.FP = ""
		code := mustEncode(t, p)
		// 这张码过不了 Validate，Decode 会直接报 ErrBadPayload。
		if _, err := Decode(code, testPub); !errors.Is(err, ErrBadPayload) {
			t.Fatalf("err = %v, want ErrBadPayload", err)
		}
	})
}

// 时间回拨超过容差时必须被判为异常。
func TestEvaluateDetectsClockRollback(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	p := basePayload(now)
	code := mustEncode(t, p)
	rec := &Record{Code: code, AppliedAt: now.Unix(), MaxExpSeen: p.EXP, LastSeenAt: now.Unix()}

	// 把系统时间往回拨 3 天，超过 1 天容差。
	rolled := now.Add(-3 * 24 * time.Hour)
	ev := Evaluate(rec, testPub, localMACs, rolled)
	if ev.State != StateInvalid {
		t.Fatalf("state = %q, want invalid (%s)", ev.State, ev.Reason)
	}
	if !strings.Contains(ev.Reason, "系统时间") {
		t.Errorf("reason %q should mention system time", ev.Reason)
	}

	// 往回拨 2 小时属于正常时区抖动，不该误伤。
	minor := now.Add(-2 * time.Hour)
	if ev := Evaluate(rec, testPub, localMACs, minor); ev.State != StateValid {
		t.Fatalf("state = %q, want valid for minor skew (%s)", ev.State, ev.Reason)
	}
}

func TestCheckRollback(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	longExp := now.AddDate(1, 0, 0).Unix()
	shortExp := now.AddDate(0, 0, 30).Unix()

	existing := &Record{MaxExpSeen: longExp}

	err := CheckRollback(existing, Payload{EXP: shortExp}, true)
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("err = %v, want ErrRollback for shorter expiry", err)
	}
	// 报错必须带上两侧日期，否则运维不知道要签多晚才能过。
	var rb *RollbackError
	if !errors.As(err, &rb) {
		t.Fatalf("err = %T, want *RollbackError", err)
	}
	if rb.Horizon != longExp || rb.Incoming != shortExp {
		t.Fatalf("rollback error = %+v, want horizon %d incoming %d", rb, longExp, shortExp)
	}
	if !strings.Contains(err.Error(), "2027-06-01") {
		t.Fatalf("error %q should mention the horizon date", err)
	}

	if err := CheckRollback(existing, Payload{EXP: longExp + 86400}, true); err != nil {
		t.Fatalf("err = %v, want nil for longer expiry", err)
	}
	if err := CheckRollback(nil, Payload{EXP: shortExp}, true); err != nil {
		t.Fatalf("err = %v, want nil when nothing applied yet", err)
	}
	if err := CheckRollback(existing, Payload{EXP: 0}, true); err != nil {
		t.Fatalf("err = %v, want nil for permanent license", err)
	}

	// 复位码：发码端持私钥明确要求清水位，必须能过。
	if err := CheckRollback(existing, Payload{EXP: shortExp, RST: true}, true); err != nil {
		t.Fatalf("err = %v, want nil for issuer reset code", err)
	}

	// 现存码不可信（换钥、损坏）：水位作废，否则用户被一张自己都验不过
	// 签的历史记录永久挡在门外。这正是 2026-11 事故的直接成因。
	if err := CheckRollback(existing, Payload{EXP: shortExp}, false); err != nil {
		t.Fatalf("err = %v, want nil when stored record is untrusted", err)
	}
}

// 历史峰值必须保留：用短期码覆盖后，回滚保护不能失效。
func TestNextRecordKeepsMaxExpSeen(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	longExp := now.AddDate(1, 0, 0).Unix()
	existing := &Record{MaxExpSeen: longExp, LastSeenAt: now.Unix()}

	rec := NextRecord(existing, "WNL1.x.y", Payload{EXP: now.AddDate(0, 0, 30).Unix()}, now, true)
	if rec.MaxExpSeen != longExp {
		t.Fatalf("MaxExpSeen = %d, want %d (peak must be preserved)", rec.MaxExpSeen, longExp)
	}
}

// 复位码与不可信旧记录都必须丢掉峰值，否则水位永远降不下来。
func TestNextRecordDropsPeak(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	longExp := now.AddDate(1, 0, 0).Unix()
	shortExp := now.AddDate(0, 0, 30).Unix()
	existing := &Record{MaxExpSeen: longExp, LastSeenAt: now.Unix()}

	// 复位码：从这张码重新起算。
	reset := NextRecord(existing, "WNL1.x.y", Payload{EXP: shortExp, RST: true}, now, true)
	if reset.MaxExpSeen != shortExp {
		t.Fatalf("MaxExpSeen = %d, want %d for reset code", reset.MaxExpSeen, shortExp)
	}

	// 旧记录不可信：它记的上限不再是事实。
	untrusted := NextRecord(existing, "WNL1.x.y", Payload{EXP: shortExp}, now, false)
	if untrusted.MaxExpSeen != shortExp {
		t.Fatalf("MaxExpSeen = %d, want %d when record untrusted", untrusted.MaxExpSeen, shortExp)
	}
}

// KeyID 只用于排障对号，要求稳定、可区分、长度固定。
func TestKeyID(t *testing.T) {
	otherPub := otherPriv.Public().(ed25519.PublicKey)

	id := KeyID(testPub)
	if len(id) != 16 {
		t.Fatalf("len(KeyID) = %d, want 16", len(id))
	}
	if again := KeyID(testPub); again != id {
		t.Fatalf("KeyID is not stable: %q vs %q", id, again)
	}
	if KeyID(otherPub) == id {
		t.Fatal("different keys must produce different KeyID")
	}
	if got := KeyID([]byte("short")); got != "" {
		t.Fatalf("KeyID(wrong size) = %q, want empty", got)
	}
}

// TouchRecord 只推进不后退，否则「每次启动回拨一点点」就能绕过检测。
func TestTouchRecordNeverGoesBackwards(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec := &Record{LastSeenAt: now.Unix()}
	TouchRecord(rec, now.Add(-48*time.Hour))
	if rec.LastSeenAt != now.Unix() {
		t.Fatalf("LastSeenAt = %d, want %d (must not move backwards)", rec.LastSeenAt, now.Unix())
	}
	TouchRecord(rec, now.Add(time.Hour))
	if rec.LastSeenAt != now.Add(time.Hour).Unix() {
		t.Fatalf("LastSeenAt = %d, want %d", rec.LastSeenAt, now.Add(time.Hour).Unix())
	}
}

// ---- Store 层测试：持久化、导入流程、原子写 ----

func newTestStore(t *testing.T, now time.Time, macs []string) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return NewStore(StoreOptions{
		Dir:       dir,
		PublicKey: testPub,
		LocalMACs: macs,
		Now:       func() time.Time { return now },
	}), dir
}

func TestStoreApplyAndPersist(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store, dir := newTestStore(t, now, localMACs)

	if ev := store.Current(); ev.State != StateMissing {
		t.Fatalf("initial state = %q, want missing", ev.State)
	}

	code := mustEncode(t, basePayload(now))
	ev, err := store.Apply(code)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ev.State != StateValid {
		t.Fatalf("state after apply = %q (%s)", ev.State, ev.Reason)
	}

	// 落盘必须真的发生，且权限是 0600。
	path := filepath.Join(dir, licenseFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat license file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fileMode {
		t.Errorf("file mode = %o, want %o", perm, fileMode)
	}

	// 新建一个 Store 模拟重启，状态必须恢复。
	reopened := NewStore(StoreOptions{
		Dir:       dir,
		PublicKey: testPub,
		LocalMACs: localMACs,
		Now:       func() time.Time { return now },
	})
	if ev := reopened.Current(); ev.State != StateValid {
		t.Fatalf("state after reopen = %q (%s)", ev.State, ev.Reason)
	}
}

// 导入失败不能破坏已有的有效授权——这是最容易写错的地方。
func TestStoreApplyFailureLeavesRecordIntact(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store, _ := newTestStore(t, now, localMACs)

	good := mustEncode(t, basePayload(now))
	if _, err := store.Apply(good); err != nil {
		t.Fatalf("apply good: %v", err)
	}

	t.Run("bad signature", func(t *testing.T) {
		bad, _ := Encode(basePayload(now), otherPriv)
		if _, err := store.Apply(bad); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v, want ErrBadSignature", err)
		}
		if ev := store.Current(); ev.State != StateValid {
			t.Fatalf("state = %q, want still valid", ev.State)
		}
	})

	t.Run("wrong machine", func(t *testing.T) {
		p := basePayload(now)
		p.MAC = []string{macOther}
		p.FP = Fingerprint([]string{macOther})
		code := mustEncode(t, p)
		if _, err := store.Apply(code); !errors.Is(err, ErrFingerprintMismatch) {
			t.Fatalf("err = %v, want ErrFingerprintMismatch", err)
		}
		if ev := store.Current(); ev.State != StateValid {
			t.Fatalf("state = %q, want still valid", ev.State)
		}
	})

	t.Run("already expired", func(t *testing.T) {
		// 到期时间必须晚于已导入的那张码，否则会先被防回滚拦下，
		// 测不到 ErrExpired。这里用一张 45 天后到期的码，再把「现在」
		// 挪到它之后来判断——但 store 的 now 是固定的，所以改为直接
		// 构造一张签发于很久以前、到期于「导入时刻之后但已过去」的码：
		// 让 EXP 落在 (当前记录 EXP, now) 之间不可行（now < 当前记录 EXP），
		// 因此改用独立的 store，没有已导入记录时验证 ErrExpired。
		fresh := NewStore(StoreOptions{
			Dir:       t.TempDir(),
			PublicKey: testPub,
			LocalMACs: localMACs,
			Now:       func() time.Time { return now },
		})
		p := basePayload(now)
		p.IAT = now.Add(-60 * 24 * time.Hour).Unix()
		p.EXP = now.Add(-24 * time.Hour).Unix()
		code := mustEncode(t, p)
		if _, err := fresh.Apply(code); !errors.Is(err, ErrExpired) {
			t.Fatalf("err = %v, want ErrExpired", err)
		}
		if ev := store.Current(); ev.State != StateValid {
			t.Fatalf("state = %q, want still valid", ev.State)
		}
	})
}

// 旧码不能覆盖新码。
func TestStoreApplyRejectsRollback(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store, _ := newTestStore(t, now, localMACs)

	long := basePayload(now)
	long.EXP = now.AddDate(1, 0, 0).Unix()
	if _, err := store.Apply(mustEncode(t, long)); err != nil {
		t.Fatalf("apply long: %v", err)
	}

	short := basePayload(now)
	short.SN = "SN-2026-0002"
	short.EXP = now.AddDate(0, 0, 30).Unix()
	if _, err := store.Apply(mustEncode(t, short)); !errors.Is(err, ErrRollback) {
		t.Fatalf("err = %v, want ErrRollback", err)
	}
}

// Evaluate 要推进 LastSeenAt，落盘后重启仍能检测回拨。
func TestStoreEvaluateAdvancesLastSeen(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	current := base
	dir := t.TempDir()
	store := NewStore(StoreOptions{
		Dir:       dir,
		PublicKey: testPub,
		LocalMACs: localMACs,
		Now:       func() time.Time { return current },
	})

	if _, err := store.Apply(mustEncode(t, basePayload(base))); err != nil {
		t.Fatalf("apply: %v", err)
	}

	current = base.Add(10 * 24 * time.Hour)
	store.Evaluate()

	// 重启后把时钟往回拨 5 天，应当被识别。
	reopened := NewStore(StoreOptions{
		Dir:       dir,
		PublicKey: testPub,
		LocalMACs: localMACs,
		Now:       func() time.Time { return base.Add(5 * 24 * time.Hour) },
	})
	if ev := reopened.Evaluate(); ev.State != StateInvalid {
		t.Fatalf("state = %q, want invalid after rollback (%s)", ev.State, ev.Reason)
	}
}

func TestStoreReset(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store, dir := newTestStore(t, now, localMACs)
	if _, err := store.Apply(mustEncode(t, basePayload(now))); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if err := store.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if ev := store.Current(); ev.State != StateMissing {
		t.Fatalf("state after reset = %q, want missing", ev.State)
	}
	// 原文件被移走，备份还在。
	if _, err := os.Stat(filepath.Join(dir, licenseFileName)); !os.IsNotExist(err) {
		t.Errorf("license file should be renamed away, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, licenseFileName+".backup")); err != nil {
		t.Errorf("backup file should exist: %v", err)
	}
}

// 损坏的授权文件应当当作「没有授权」处理，且不能被删除。
func TestStoreCorruptFileTreatedAsMissing(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, licenseFileName), []byte("{not json"), fileMode); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	store := NewStore(StoreOptions{
		Dir:       dir,
		PublicKey: testPub,
		LocalMACs: localMACs,
		Now:       func() time.Time { return now },
	})
	if ev := store.Current(); ev.State != StateMissing {
		t.Fatalf("state = %q, want missing", ev.State)
	}
	if _, err := os.Stat(filepath.Join(dir, licenseFileName)); err != nil {
		t.Errorf("corrupt file must not be deleted: %v", err)
	}
}

func TestMachineFingerprintHelper(t *testing.T) {
	fp, macs := MachineFingerprint([]string{"AA-BB-CC-DD-EE-01", macWired})
	if len(macs) != 2 {
		t.Fatalf("macs = %v, want 2 normalized entries", macs)
	}
	if fp != Fingerprint(macs) {
		t.Fatalf("fp = %q, want %q", fp, Fingerprint(macs))
	}
}

// 本机没有任何可用物理网卡时，必须给出「联系运维」的明确指引，
// 而不是笼统的「机器不一致」——后者会让用户去反复核对网卡，
// 而虚拟化环境里的用户根本无从改起。
func TestEvaluateNoUsableMAC(t *testing.T) {
	priv := testPriv
	pub := testPub
	payload := Payload{
		V:    1,
		SN:   "SN-NO-MAC",
		FP:   Fingerprint([]string{macWiFi}),
		MAC:  []string{macWiFi},
		IAT:  time.Now().Add(-time.Hour).Unix(),
		EXP:  time.Now().Add(24 * time.Hour).Unix(),
		Feat: []Feature{FeatureKB},
	}
	code, err := Encode(payload, priv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	record := &Record{Code: code}

	ev := Evaluate(record, pub, nil, time.Now())
	if ev.State != StateInvalid {
		t.Fatalf("state = %s, want invalid", ev.State)
	}
	if !strings.Contains(ev.Reason, "物理网卡") {
		t.Errorf("reason should explain the missing NIC, got %q", ev.Reason)
	}
	if !strings.Contains(ev.Reason, "运维") {
		t.Errorf("reason should tell the user who to contact, got %q", ev.Reason)
	}
}

// 相反地，有网卡但不匹配时，提示语里不该出现「没有物理网卡」，
// 否则用户会以为硬件坏了。
func TestEvaluateMachineMismatchHasDistinctReason(t *testing.T) {
	priv := testPriv
	pub := testPub
	payload := Payload{
		V:    1,
		SN:   "SN-OTHER-MACHINE",
		FP:   Fingerprint([]string{macOther}),
		MAC:  []string{macOther},
		IAT:  time.Now().Add(-time.Hour).Unix(),
		EXP:  time.Now().Add(24 * time.Hour).Unix(),
		Feat: []Feature{FeatureKB},
	}
	code, err := Encode(payload, priv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	ev := Evaluate(&Record{Code: code}, pub, []string{macWiFi}, time.Now())
	if ev.State != StateInvalid {
		t.Fatalf("state = %s, want invalid", ev.State)
	}
	if strings.Contains(ev.Reason, "没有可用于授权的物理网卡") {
		t.Errorf("mismatch reason must not claim the machine lacks a NIC: %q", ev.Reason)
	}
}

// 运维逃生舱：采不到物理网卡时才允许用环境变量指定锚点。
func TestMACOverrideOnlyWhenNoPhysicalNIC(t *testing.T) {
	t.Setenv("WK_LICENSE_MAC_OVERRIDE", "3c:22:fb:aa:bb:cc")

	// 直接测解析函数，避免依赖测试机真实网卡情况。
	if got := normalizeMACOverride("3c:22:fb:aa:bb:cc"); len(got) != 1 || got[0] != "3c:22:fb:aa:bb:cc" {
		t.Errorf("single MAC override: %v", got)
	}
	if got := normalizeMACOverride("3c:22:fb:aa:bb:cc; f0:18:98:aa:bb:cc"); len(got) != 2 {
		t.Errorf("multi MAC override: %v", got)
	}
	// 非法值必须丢弃，宁可变「没有网卡」也不能变成「任意匹配」。
	if got := normalizeMACOverride("not-a-mac"); len(got) != 0 {
		t.Errorf("invalid override must yield nothing, got %v", got)
	}
	// 全零与本地管理地址同样不接受。
	if got := normalizeMACOverride("00:00:00:00:00:00"); len(got) != 0 {
		t.Errorf("all-zero MAC must be rejected, got %v", got)
	}
	// 本地管理地址**接受**：macOS 的 Wi-Fi 私有地址、VPN 改写后的地址都
	// 带这个标记位，拒绝它等于让整台机器变成「无网卡」。见 IsUsableMAC。
	if got := normalizeMACOverride("02:00:00:00:00:00"); len(got) != 1 {
		t.Errorf("locally administered MAC must be accepted, got %v", got)
	}
	if got := normalizeMACOverride("01:00:5e:00:00:01"); len(got) != 0 {
		t.Errorf("multicast MAC must be rejected, got %v", got)
	}
	if got := normalizeMACOverride(""); len(got) != 0 {
		t.Errorf("empty override must yield nothing, got %v", got)
	}
}

// ---- 换钥事故回归（2026-11）----
//
// 事故链条：发码端数据目录变更 → 静默生成新密钥 → 客户端打包注入了新公钥
// → 历史长码验签失败（invalid）→ 但旧记录的 MaxExpSeen 仍然生效 → 新签的
// 短期码被回滚保护拒绝 → 用户被锁死且无自助出路。
//
// 这两条测试正是对「水位必须与码的可信度绑定」的行为锁定。

// sealRecord 把一条记录写进 store 目录，模拟「本机已经导入过某张码」。
func sealRecord(t *testing.T, dir string, rec Record) {
	t.Helper()
	data, err := json.Marshal(&rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, licenseFileName), data, 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

func TestStoreApplyAfterKeyRotation(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store, dir := newTestStore(t, now, localMACs)

	// 旧密钥签的长码：签名对不上当前公钥，但水位记录是真实的。
	longExp := now.AddDate(1, 0, 0).Unix()
	oldPayload := basePayload(now)
	oldPayload.EXP = longExp
	oldCode, err := Encode(oldPayload, otherPriv)
	if err != nil {
		t.Fatalf("encode old code: %v", err)
	}
	sealRecord(t, dir, Record{
		Code:       oldCode,
		AppliedAt:  now.Unix(),
		MaxExpSeen: longExp,
		LastSeenAt: now.Unix(),
	})

	// 用户看到的第一条提示：验签失败。
	if ev := store.Current(); ev.State != StateInvalid {
		t.Fatalf("state = %q, want invalid for code signed by rotated key", ev.State)
	}

	// 新码到期更早，但签名有效——必须能导入。
	ev, outcome, err := store.ApplyWithOutcome(mustEncode(t, basePayload(now)))
	if err != nil {
		t.Fatalf("apply after key rotation: %v (must not be blocked by dead watermark)", err)
	}
	if ev.State != StateValid {
		t.Fatalf("state = %q (%s), want valid", ev.State, ev.Reason)
	}
	if outcome.Reason != ResetReasonUntrusted {
		t.Fatalf("outcome.Reason = %q, want %q", outcome.Reason, ResetReasonUntrusted)
	}
	if outcome.HorizonFrom != longExp {
		t.Fatalf("outcome.HorizonFrom = %d, want %d", outcome.HorizonFrom, longExp)
	}
	if outcome.KeyID != "" {
		t.Fatalf("outcome.KeyID = %q, want empty for code without kid", outcome.KeyID)
	}

	// 水位必须落到新码的到期时间，不能还留着旧峰值。
	saved := store.Current().Payload
	if saved == nil || saved.EXP != basePayload(now).EXP {
		t.Fatalf("saved payload exp = %v, want %d", saved, basePayload(now).EXP)
	}
}

// 旧记录仍然可信时，水位照旧生效——修复不能把回滚防护一起废掉。
func TestStoreApplyStillBlocksRollback(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store, dir := newTestStore(t, now, localMACs)

	longExp := now.AddDate(1, 0, 0).Unix()
	longPayload := basePayload(now)
	longPayload.EXP = longExp
	sealRecord(t, dir, Record{
		Code:       mustEncode(t, longPayload),
		AppliedAt:  now.Unix(),
		MaxExpSeen: longExp,
		LastSeenAt: now.Unix(),
	})

	_, _, err := store.ApplyWithOutcome(mustEncode(t, basePayload(now)))
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("err = %v, want ErrRollback when stored record is still trusted", err)
	}
}

// 复位码：持私钥签发即可主动降水位，这是唯一被允许的复位通道。
func TestStoreApplyIssuerReset(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store, dir := newTestStore(t, now, localMACs)

	longExp := now.AddDate(1, 0, 0).Unix()
	longPayload := basePayload(now)
	longPayload.EXP = longExp
	sealRecord(t, dir, Record{
		Code:       mustEncode(t, longPayload),
		AppliedAt:  now.Unix(),
		MaxExpSeen: longExp,
		LastSeenAt: now.Unix(),
	})

	resetPayload := basePayload(now)
	resetPayload.RST = true
	ev, outcome, err := store.ApplyWithOutcome(mustEncode(t, resetPayload))
	if err != nil {
		t.Fatalf("apply reset code: %v", err)
	}
	if ev.State != StateValid {
		t.Fatalf("state = %q (%s), want valid", ev.State, ev.Reason)
	}
	if outcome.Reason != ResetReasonIssuer {
		t.Fatalf("outcome.Reason = %q, want %q", outcome.Reason, ResetReasonIssuer)
	}
	if outcome.HorizonFrom != longExp {
		t.Fatalf("outcome.HorizonFrom = %d, want %d", outcome.HorizonFrom, longExp)
	}
	if ev.Payload == nil || !ev.Payload.RST {
		t.Fatal("applied payload should carry RST so audit can tell it apart")
	}
}
