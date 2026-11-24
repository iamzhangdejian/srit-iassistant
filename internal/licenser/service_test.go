package licenser

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// 与 license 包一致的测试用机器 MAC。
const (
	macA = "3c:22:fb:dd:ee:01"
	macB = "f0:18:98:dd:ee:02"
)

func testKeyPair(seed byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed + byte(i)
	}
	priv := ed25519.NewKeyFromSeed(s)
	return priv.Public().(ed25519.PublicKey), priv
}

// newLicensedService 构造一个「已授权」的服务。
//
// exp 是授权到期时间。为了让「已过期」的用例可以测，签发时间固定在
// 2026-01-01，而到期时间由调用方给——Apply 会拒绝导入「当前已过期」
// 的码，所以测试里通过可推进的时钟模拟「码是用了一阵子才过期的」。
func newLicensedService(t *testing.T, features []license.Feature, exp time.Time) *Service {
	t.Helper()
	return newServiceWithClock(t, features, exp,
		time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC))
}

// newServiceWithClock 用指定「当前时间」构造服务，用于测过期降级。
func newServiceWithClock(t *testing.T, features []license.Feature, exp, now time.Time) *Service {
	t.Helper()
	pub, priv := testKeyPair(1)
	store := license.NewStore(license.StoreOptions{
		Dir:       t.TempDir(),
		PublicKey: pub,
		LocalMACs: []string{macA, macB},
		// 导入时用「码还没过期」的时间，模拟用户正常激活。
		Now: func() time.Time { return now },
	})

	payload := license.Payload{
		V:    1,
		SN:   "SN-TEST",
		FP:   license.Fingerprint([]string{macA, macB}),
		MAC:  []string{macA, macB},
		IAT:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		EXP:  exp.Unix(),
		Feat: features,
	}
	code, err := license.Encode(payload, priv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := store.Apply(code); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return NewService(Options{Store: store, Enabled: true})
}

// 未启用闸门时一切都放行，且 HasFeature 恒为 true。
func TestGateDisabledAllowsEverything(t *testing.T) {
	svc := NewService(Options{Enabled: false})
	for _, path := range []string{"/api/v1/agent-chat/x", "/api/v1/artifacts", "/api/v1/knowledge"} {
		if d := svc.Gate(path); !d.Allowed {
			t.Errorf("%s should be allowed when license gate is off", path)
		}
	}
	if !svc.HasFeature(license.FeatureOffice) {
		t.Error("HasFeature must be true when the gate is off")
	}
}

// 没有授权码时，业务接口全拒，但登录/授权页/健康检查必须放行。
func TestGateMissingBlocksBusinessButAllowsBootstrap(t *testing.T) {
	pub, _ := testKeyPair(1)
	svc := NewService(Options{
		Store: license.NewStore(license.StoreOptions{
			Dir:       t.TempDir(),
			PublicKey: pub,
			LocalMACs: []string{macA},
		}),
		Enabled: true,
	})
	if svc.Status().State != license.StateMissing {
		t.Fatalf("state = %s, want missing", svc.Status().State)
	}

	business := []string{
		"/api/v1/agent-chat/abc",
		"/api/v1/knowledge-bases",
		"/api/v1/artifacts",
	}
	for _, p := range business {
		d := svc.Gate(p)
		if d.Allowed {
			t.Errorf("%s must be blocked without a license", p)
		}
		if d.Status != 403 {
			t.Errorf("%s status = %d, want 403 (401 would trap the frontend in a login loop)", p, d.Status)
		}
		if d.Code != CodeLicenseMissing {
			t.Errorf("%s code = %s", p, d.Code)
		}
	}

	// 这些必须放行，否则用户永远进不了授权页。
	bootstrap := []string{
		"/api/v1/license",
		"/api/v1/auth/login",
		"/api/v1/auth/auto-setup",
		"/api/v1/auth/config",
		"/api/v1/auth/refresh",
		"/api/v1/auth/me",
		"/health",
	}
	for _, p := range bootstrap {
		if !svc.Gate(p).Allowed {
			t.Errorf("%s must stay reachable without a license (否则授权页打不开)", p)
		}
	}
}

// 前缀匹配不能把 /api/v1/licenseevil 当成 /api/v1/license 放行。
func TestIsAllowedPathRequiresSegmentBoundary(t *testing.T) {
	if IsAllowedPath("/api/v1/licenseevil") {
		t.Error("/api/v1/licenseevil must not match /api/v1/license")
	}
	// 授权接口没有子资源；子路径一律不放行，避免将来有人加个
	// /api/v1/license/export 之类的接口时无意中把它暴露在闸门外。
	if IsAllowedPath("/api/v1/license/anything") {
		t.Error("subpaths of the license endpoint must not be auto-allowed")
	}
	if !IsAllowedPath("/api/v1/license") {
		t.Error("/api/v1/license must be allowed")
	}
}

// 授权有效时业务接口放行。
func TestGateValidLicenseAllowsAll(t *testing.T) {
	svc := newLicensedService(t,
		[]license.Feature{license.FeatureKB, license.FeatureChat, license.FeatureOCR, license.FeatureOffice},
		time.Date(2027, 11, 20, 0, 0, 0, 0, time.UTC))
	if got := svc.Status().State; got != license.StateValid {
		t.Fatalf("state = %s, want valid", got)
	}
	for _, p := range []string{"/api/v1/agent-chat/x", "/api/v1/artifacts", "/api/v1/skills"} {
		if d := svc.Gate(p); !d.Allowed {
			t.Errorf("%s should be allowed: %+v", p, d)
		}
	}
}

// 功能位缺失时只关掉对应接口，别的照常。
func TestGateFeatureDenied(t *testing.T) {
	// 只给知识库，不给文档生成。
	svc := newLicensedService(t,
		[]license.Feature{license.FeatureKB, license.FeatureChat},
		time.Date(2027, 11, 20, 0, 0, 0, 0, time.UTC))

	if d := svc.Gate("/api/v1/artifacts"); d.Allowed || d.Code != CodeFeatureDenied {
		t.Errorf("artifacts should be denied by feature bitmap: %+v", d)
	}
	if d := svc.Gate("/api/v1/skills"); d.Allowed || d.Code != CodeFeatureDenied {
		t.Errorf("skills should be denied by feature bitmap: %+v", d)
	}
	if d := svc.Gate("/api/v1/knowledge-bases"); !d.Allowed {
		t.Errorf("knowledge bases should still work: %+v", d)
	}
	if d := svc.Gate("/api/v1/agent-chat/x"); !d.Allowed {
		t.Errorf("chat should still work: %+v", d)
	}
}

// 过期降级：读资料照旧，文档生成停用，且必须提示「续期可恢复」。
func TestGateExpiredDegradesToReadOnly(t *testing.T) {
	pub, priv := testKeyPair(1)
	exp := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	// 激活时刻在到期之前，之后把时钟拨到宽限期之外。
	activatedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := exp.Add(30 * 24 * time.Hour)

	store := license.NewStore(license.StoreOptions{
		Dir:       t.TempDir(),
		PublicKey: pub,
		LocalMACs: []string{macA, macB},
		Now:       func() time.Time { return activatedAt },
	})
	payload := license.Payload{
		V:    1,
		SN:   "SN-EXPIRED",
		FP:   license.Fingerprint([]string{macA, macB}),
		MAC:  []string{macA, macB},
		IAT:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		EXP:  exp.Unix(),
		Feat: []license.Feature{license.FeatureKB, license.FeatureChat, license.FeatureOCR, license.FeatureOffice},
	}
	code, err := license.Encode(payload, priv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := store.Apply(code); err != nil {
		t.Fatalf("apply (should succeed before expiry): %v", err)
	}

	// 时间推进到宽限期之后，重新判定。
	store.SetNowFunc(func() time.Time { return now })
	svc := NewService(Options{Store: store, Enabled: true})
	svc.Refresh()

	ev := svc.Status()
	if ev.State != license.StateReadOnly {
		t.Fatalf("state = %s, want readonly", ev.State)
	}

	// 查资料必须仍然可用——用户不能因为没续费就看不到自己的知识库。
	for _, p := range []string{"/api/v1/knowledge-bases", "/api/v1/sessions", "/api/v1/knowledge-chat/x"} {
		if d := svc.Gate(p); !d.Allowed {
			t.Errorf("%s must stay readable after expiry: %+v", p, d)
		}
	}

	// 生成类能力停用。
	for _, p := range []string{"/api/v1/artifacts", "/api/v1/skills"} {
		d := svc.Gate(p)
		if d.Allowed {
			t.Errorf("%s must be denied after expiry", p)
		}
		if d.Code != CodeLicenseExpired {
			t.Errorf("%s code = %s, want %s", p, d.Code, CodeLicenseExpired)
		}
		if d.Message == "" {
			t.Errorf("%s must explain how to restore the feature", p)
		}
	}
}

// Apply 的错误分类必须可区分，前端据此给不同引导。
func TestApplyErrorMessages(t *testing.T) {
	svc := newLicensedService(t, []license.Feature{license.FeatureKB},
		time.Date(2027, 11, 20, 0, 0, 0, 0, time.UTC))

	if _, err := svc.Apply("not-a-code"); err == nil {
		t.Fatal("garbage code must fail")
	}

	// 别的机器签出的码（MAC 不匹配）。
	_, otherPriv := testKeyPair(200)
	otherPub := otherPriv.Public().(ed25519.PublicKey)
	_ = otherPub
	payload := license.Payload{
		V:    1,
		SN:   "SN-OTHER",
		FP:   license.Fingerprint([]string{"11:22:33:44:55:66"}),
		MAC:  []string{"11:22:33:44:55:66"},
		IAT:  time.Now().Add(-time.Hour).Unix(),
		EXP:  time.Now().Add(24 * time.Hour).Unix(),
		Feat: []license.Feature{license.FeatureKB},
	}
	code, err := license.Encode(payload, otherPriv)
	if err != nil {
		t.Fatalf("encode other: %v", err)
	}
	// 用本机公钥解析必然签名失败（不同密钥对），也算「格式/签名」类错误。
	if _, err := svc.Apply(code); err == nil {
		t.Fatal("foreign-signed code must fail")
	}
}

// Enabled() 对 nil 服务必须安全返回 false。
func TestNilServiceIsSafe(t *testing.T) {
	var svc *Service
	if svc.Enabled() {
		t.Error("nil service must report disabled")
	}
	if svc.Status().State != license.StateMissing {
		t.Error("nil service must report missing state")
	}
}

// AllowSessionIssue 与 Gate 必须给出不同答案：login 在 Gate 眼里是
// 「引导路径」要放行，但在 AllowSessionIssue 眼里「不能发令牌」。
// 这条区分一旦丢掉，授权闸门在登录口就完全失效——而现象是「一切
// 正常」，很难发现。
func TestAllowSessionIssueDistinctFromGate(t *testing.T) {
	pub, _ := testKeyPair(1)
	missing := NewService(Options{
		Store: license.NewStore(license.StoreOptions{
			Dir:       t.TempDir(),
			PublicKey: pub,
			LocalMACs: []string{macA},
		}),
		Enabled: true,
	})

	// 请求可以到达处理器。
	if !missing.Gate("/api/v1/auth/login").Allowed {
		t.Fatal("login request must reach the handler so the user sees why")
	}
	// 但不能真的登录。
	if code, msg := missing.AllowSessionIssue(); code == "" || msg == "" {
		t.Fatal("must not issue tokens without a license")
	}

	// 授权有效时允许登录。
	valid := newLicensedService(t, []license.Feature{license.FeatureKB},
		time.Date(2027, 11, 20, 0, 0, 0, 0, time.UTC))
	if code, _ := valid.AllowSessionIssue(); code != "" {
		t.Fatalf("valid license must allow session issue, got %s", code)
	}
}

// 过期降级后仍然允许登录——用户必须能进来看自己的资料。
func TestAllowSessionIssueAfterExpiry(t *testing.T) {
	pub, priv := testKeyPair(1)
	exp := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store := license.NewStore(license.StoreOptions{
		Dir:       t.TempDir(),
		PublicKey: pub,
		LocalMACs: []string{macA},
		Now:       func() time.Time { return exp.Add(-24 * time.Hour) },
	})
	code, err := license.Encode(license.Payload{
		V: 1, SN: "SN-EXP", FP: license.Fingerprint([]string{macA}),
		MAC: []string{macA}, IAT: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
		EXP: exp.Unix(), Feat: []license.Feature{license.FeatureKB},
	}, priv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := store.Apply(code); err != nil {
		t.Fatalf("apply: %v", err)
	}

	store.SetNowFunc(func() time.Time { return exp.Add(60 * 24 * time.Hour) })
	svc := NewService(Options{Store: store, Enabled: true})
	svc.Refresh()
	if got := svc.Status().State; got != license.StateReadOnly {
		t.Fatalf("state = %s, want readonly", got)
	}
	if c, _ := svc.AllowSessionIssue(); c != "" {
		t.Error("expired license must still allow login (read-only mode)")
	}
}

// 会话列表不属于对话能力：只买知识库的客户仍要能看到自己的历史会话，
// 但真正发起问答（agent-chat / knowledge-chat）必须被挡。
func TestSessionsNotGatedByChatFeature(t *testing.T) {
	svc := newLicensedService(t, []license.Feature{license.FeatureKB},
		time.Date(2027, 11, 20, 0, 0, 0, 0, time.UTC))

	if d := svc.Gate("/api/v1/sessions"); !d.Allowed {
		t.Errorf("/api/v1/sessions must stay reachable with a KB-only license: %+v", d)
	}
	if d := svc.Gate("/api/v1/sessions/abc/messages"); !d.Allowed {
		t.Errorf("/api/v1/sessions/{id}/messages must stay reachable: %+v", d)
	}
	for _, p := range []string{"/api/v1/agent-chat/abc", "/api/v1/knowledge-chat/abc"} {
		if d := svc.Gate(p); d.Allowed || d.Code != CodeFeatureDenied {
			t.Errorf("%s must be denied without the chat feature: %+v", p, d)
		}
	}
}

// 定时重判：应用长期不重启时，授权到期必须能自动生效。
//
// 这是第四期最关键的硬化项。没有它，用户把应用开三周，授权码即使
// 早已过期，程序仍认为有效——商业上等于白送。
func TestWatcherTickDegradesOnExpiry(t *testing.T) {
	pub, priv := testKeyPair(1)
	exp := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	activatedAt := exp.Add(-30 * 24 * time.Hour)

	store := license.NewStore(license.StoreOptions{
		Dir:       t.TempDir(),
		PublicKey: pub,
		LocalMACs: []string{macA},
		Now:       func() time.Time { return activatedAt },
	})
	code, err := license.Encode(license.Payload{
		V: 1, SN: "SN-TICK", FP: license.Fingerprint([]string{macA}),
		MAC: []string{macA}, IAT: activatedAt.Unix(), EXP: exp.Unix(),
		Feat: []license.Feature{license.FeatureKB, license.FeatureOffice},
	}, priv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := store.Apply(code); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var transitions []string
	svc := NewService(Options{
		Store:   store,
		Enabled: true,
		OnStateChange: func(prev, next license.Evaluation) {
			transitions = append(transitions, string(prev.State)+"->"+string(next.State))
		},
	})
	if got := svc.Status().State; got != license.StateValid {
		t.Fatalf("initial state = %s, want valid", got)
	}

	// 时间推进到过期之后（超出宽限期），模拟「应用一直开着」。
	store.SetNowFunc(func() time.Time { return exp.Add(60 * 24 * time.Hour) })
	svc.tick()

	if got := svc.Status().State; got != license.StateReadOnly {
		t.Fatalf("after tick state = %s, want readonly", got)
	}
	if len(transitions) != 1 || transitions[0] != "valid->readonly" {
		t.Fatalf("transitions = %v, want [valid->readonly]", transitions)
	}
	// 降级后文档生成必须停用，知识库仍可用。
	if svc.Gate("/api/v1/artifacts").Allowed {
		t.Error("artifacts must be denied after expiring")
	}
	if !svc.Gate("/api/v1/knowledge-bases").Allowed {
		t.Error("knowledge-bases must stay reachable after expiring")
	}
}

// valid -> valid 的常规重判不应产生回调，否则审计日志会被噪声淹没。
func TestWatcherTickNoCallbackWhenStable(t *testing.T) {
	var calls int
	svc := newLicensedServiceWithCallback(t, []license.Feature{license.FeatureKB},
		time.Date(2027, 11, 20, 0, 0, 0, 0, time.UTC), func(_, _ license.Evaluation) { calls++ })

	svc.tick()
	svc.tick()
	if calls != 0 {
		t.Fatalf("stable state must not fire callbacks, got %d", calls)
	}
}

// 闸门未启用时 StartWatcher 不应起 goroutine，tick 也不做事。
func TestWatcherDisabledIsNoop(t *testing.T) {
	svc := NewService(Options{Enabled: false})
	svc.StartWatcher() // 不应 panic
	svc.StopWatcher()
	svc.tick()
	if svc.Enabled() {
		t.Fatal("disabled service must report Enabled()=false")
	}
}

// StopWatcher 重复调用必须安全（进程退出路径可能被多次触发）。
func TestStopWatcherIdempotent(t *testing.T) {
	svc := newLicensedService(t, []license.Feature{license.FeatureKB},
		time.Date(2027, 11, 20, 0, 0, 0, 0, time.UTC))
	svc.StartWatcher()
	svc.StopWatcher()
	svc.StopWatcher()
}

// newLicensedServiceWithCallback 与 newLicensedService 相同，但挂上状态跃迁回调。
func newLicensedServiceWithCallback(
	t *testing.T, features []license.Feature, exp time.Time,
	onChange func(prev, next license.Evaluation),
) *Service {
	t.Helper()
	base := newLicensedService(t, features, exp)
	// 复用同一个 store（已导入好授权码），只替换回调与缓存。
	svc := NewService(Options{Store: base.store, Enabled: true, OnStateChange: onChange})
	return svc
}

// 失败态服务（公钥损坏/授权目录不可用）必须「保持启用 + 业务全拒」。
//
// 这条用例守住的是一个安全不变量：一旦 Enabled() 退化成 false，中间件
// 会直接放行所有请求，日志里却还写着「refusing to start」——现象是
// 一切正常，问题要到客户发现「没授权也能用」时才暴露。
func TestFailedServiceBlocksBusiness(t *testing.T) {
	svc := NewFailed("授权公钥无效")
	if !svc.Enabled() {
		t.Fatal("failed service must stay enabled, otherwise middleware bypasses it")
	}

	ev := svc.Status()
	if ev.State != license.StateInvalid {
		t.Errorf("state = %s, want invalid", ev.State)
	}
	if !strings.Contains(ev.Reason, "授权公钥无效") {
		t.Errorf("reason = %q, want it to keep the cause", ev.Reason)
	}

	// 业务接口必须被拦，引导路径必须放行。
	if d := svc.Gate("/api/v1/agent-chat/s1"); d.Allowed {
		t.Error("business request must be blocked in failed state")
	}
	if d := svc.Gate("/api/v1/license"); !d.Allowed {
		t.Error("license bootstrap path must stay reachable")
	}

	// 失败态不能登录：否则用户进到空系统，比看到授权页更困惑。
	if code, _ := svc.AllowSessionIssue(); code == "" {
		t.Error("failed service must not issue sessions")
	}

	// 也不能导入授权码：没有 store，验签必然失败，应当直接给出原因。
	if _, err := svc.Apply("WNL1.x.y"); err == nil {
		t.Error("apply must fail without a store")
	}

	// 重判必须保留原因，否则定时器每小时会把它冲成「无授权」。
	if next := svc.Refresh(); next.State != license.StateInvalid || next.Reason != ev.Reason {
		t.Errorf("refresh changed failed state: %s / %q", next.State, next.Reason)
	}

	// 零值 Service 仍然是「未启用」，避免把误构造的零值当成闸门。
	var zero Service
	if zero.Enabled() {
		t.Error("zero value service must report disabled")
	}
}
