package licadmin

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// newTestAuth 建一个临时账号库，用完自动关。
func newTestAuth(t *testing.T) *AuthStore {
	t.Helper()
	a, err := OpenAuthStore(filepath.Join(t.TempDir(), "licadmin.db"))
	if err != nil {
		t.Fatalf("打开账号库失败：%v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name     string
		username string
		password string
		wantErr  bool
	}{
		{"太短", "admin", "Abc123456", true},
		{"纯字母", "admin", "abcdefghijk", true},
		{"纯数字", "admin", "1234567890", true},
		{"与账号相同", "admin1234", "admin1234", true},
		{"合规", "admin", "LicAdmin2026", false},
	}
	for _, c := range cases {
		err := ValidatePassword(c.username, c.password)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s：期望错误=%v，实际 %v", c.name, c.wantErr, err)
		}
	}
}

func TestCreateAdminOnlyOnce(t *testing.T) {
	a := newTestAuth(t)
	ctx := context.Background()

	setup, err := a.SetupRequired(ctx)
	if err != nil {
		t.Fatalf("查询初始化状态失败：%v", err)
	}
	if !setup {
		t.Fatal("空库应处于「需要初始化」状态")
	}
	if err := a.CreateAdmin(ctx, "admin", "LicAdmin2026"); err != nil {
		t.Fatalf("创建管理员失败：%v", err)
	}
	setup, err = a.SetupRequired(ctx)
	if err != nil || setup {
		t.Fatalf("创建后不应再需要初始化：setup=%v err=%v", setup, err)
	}
	err = a.CreateAdmin(ctx, "second", "LicAdmin2026")
	if !errors.Is(err, ErrAuthExists) {
		t.Fatalf("重复初始化应返回 ErrAuthExists，实际 %v", err)
	}
}

func TestAuthenticateAndLockout(t *testing.T) {
	a := newTestAuth(t)
	ctx := context.Background()
	if err := a.CreateAdmin(ctx, "admin", "LicAdmin2026"); err != nil {
		t.Fatalf("创建管理员失败：%v", err)
	}

	now := time.Now()
	if _, err := a.Authenticate(ctx, "admin", "LicAdmin2026", now); err != nil {
		t.Fatalf("正确密码应登录成功：%v", err)
	}
	// 账号大小写不敏感，避免「Admin/admin 记不清」这种无意义的失败。
	if _, err := a.Authenticate(ctx, "ADMIN", "LicAdmin2026", now); err != nil {
		t.Fatalf("账号应大小写不敏感：%v", err)
	}

	// 连续输错到阈值：前 4 次报剩余次数，第 5 次直接锁定。
	for i := 1; i <= 4; i++ {
		_, err := a.Authenticate(ctx, "admin", "wrong-pass", now)
		var bad *BadCredentialsError
		if !errors.As(err, &bad) {
			t.Fatalf("第 %d 次错误应返回 BadCredentialsError，实际 %v", i, err)
		}
		if bad.Remaining != freeAttempts-i {
			t.Fatalf("第 %d 次剩余次数应为 %d，实际 %d", i, freeAttempts-i, bad.Remaining)
		}
	}
	_, err := a.Authenticate(ctx, "admin", "wrong-pass", now)
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("第 %d 次错误应触发锁定，实际 %v", freeAttempts, err)
	}
	if locked.Remain <= 0 || locked.Remain > lockBase+2*time.Second {
		t.Fatalf("首次锁定时长应接近 %v，实际 %v", lockBase, locked.Remain)
	}

	// 锁定期间即使密码正确也要拒绝：否则锁定形同虚设。
	if _, err := a.Authenticate(ctx, "admin", "LicAdmin2026", now); !errors.As(err, &locked) {
		t.Fatalf("锁定期间正确密码也应被拒，实际 %v", err)
	}

	// 锁定到期后可以恢复，且计数清零（不会「一错就再锁」）。
	later := now.Add(lockBase + time.Second)
	if _, err := a.Authenticate(ctx, "admin", "LicAdmin2026", later); err != nil {
		t.Fatalf("锁定到期后应能登录：%v", err)
	}
	// 成功登录会把计数清零，之后的错误重新从「还能试 4 次」开始。
	if _, err := a.Authenticate(ctx, "admin", "wrong-pass", later); err == nil {
		t.Fatal("错误密码不应通过")
	} else {
		var bad *BadCredentialsError
		if !errors.As(err, &bad) || bad.Remaining != freeAttempts-1 {
			t.Fatalf("成功登录后计数应清零，实际 %v", err)
		}
	}
}

func TestLockoutBacksOffExponentially(t *testing.T) {
	a := newTestAuth(t)
	ctx := context.Background()
	if err := a.CreateAdmin(ctx, "admin", "LicAdmin2026"); err != nil {
		t.Fatalf("创建管理员失败：%v", err)
	}
	now := time.Now()
	for i := 0; i < freeAttempts; i++ {
		_, _ = a.Authenticate(ctx, "admin", "wrong", now)
	}
	// 锁定过期后再错一次：这次是第 6 次，应锁 2 分钟而不是 1 分钟。
	after := now.Add(lockBase + time.Second)
	_, err := a.Authenticate(ctx, "admin", "wrong", after)
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("应再次锁定，实际 %v", err)
	}
	if locked.Remain < lockBase {
		t.Fatalf("第 6 次失败应锁更久（≥%v），实际 %v", lockBase, locked.Remain)
	}
	if locked.Remain > lockMax+2*time.Second {
		t.Fatalf("锁定不应超过上限 %v，实际 %v", lockMax, locked.Remain)
	}
}

func TestChangePasswordRotatesSessionKey(t *testing.T) {
	a := newTestAuth(t)
	ctx := context.Background()
	if err := a.CreateAdmin(ctx, "admin", "LicAdmin2026"); err != nil {
		t.Fatalf("创建管理员失败：%v", err)
	}
	keyBefore, err := a.SessionKey(ctx)
	if err != nil {
		t.Fatalf("读取会话密钥失败：%v", err)
	}
	now := time.Now()
	oldToken := SignSession(keyBefore, 1, now.Add(time.Hour))

	if err := a.ChangePassword(ctx, "admin", "wrong-current", "LicAdmin2027", now); err == nil {
		t.Fatal("旧密码不正确时应失败")
	}
	if err := a.ChangePassword(ctx, "admin", "LicAdmin2026", "short", now); err == nil {
		t.Fatal("新密码不满足策略时应失败")
	}
	if err := a.ChangePassword(ctx, "admin", "LicAdmin2026", "LicAdmin2027", now); err != nil {
		t.Fatalf("改密失败：%v", err)
	}

	keyAfter, err := a.SessionKey(ctx)
	if err != nil {
		t.Fatalf("读取会话密钥失败：%v", err)
	}
	if _, ok := ParseSession(keyAfter, oldToken, now); ok {
		t.Fatal("改密后旧会话令牌应失效（密钥已轮换）")
	}
	if _, err := a.Authenticate(ctx, "admin", "LicAdmin2026", now); err == nil {
		t.Fatal("旧密码不应再能登录")
	}
	if _, err := a.Authenticate(ctx, "admin", "LicAdmin2027", now); err != nil {
		t.Fatalf("新密码应能登录：%v", err)
	}
}

func TestSessionTokenRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now()
	tok := SignSession(key, 1, now.Add(time.Hour))

	exp, ok := ParseSession(key, tok, now)
	if !ok || exp.Unix() != now.Add(time.Hour).Unix() {
		t.Fatalf("令牌应能解析：ok=%v exp=%v", ok, exp)
	}
	if _, ok := ParseSession(key, tok, now.Add(2*time.Hour)); ok {
		t.Fatal("过期令牌应被拒绝")
	}
	if _, ok := ParseSession([]byte("another-key-another-key-another!"), tok, now); ok {
		t.Fatal("换密钥后旧令牌应被拒绝")
	}
	// 篡改签名、篡改载荷都必须失败。
	if _, ok := ParseSession(key, tok+"x", now); ok {
		t.Fatal("签名被改动应失败")
	}
	if _, ok := ParseSession(key, "eyJ1aWQiOjEsImV4cCI6OTk5OTk5OTk5OX0."+tok[len(tok)-20:], now); ok {
		t.Fatal("载荷被替换应失败")
	}
}

func TestAuditAppendAndList(t *testing.T) {
	a := newTestAuth(t)
	ctx := context.Background()
	a.AppendAudit(ctx, AuditEntry{Actor: "admin", Action: "登录成功", IP: "127.0.0.1"})
	a.AppendAudit(ctx, AuditEntry{Actor: "admin", Action: "签发授权码", Target: "SN-1", Detail: "续 365 天"})

	entries, err := a.ListAudit(ctx, 10)
	if err != nil {
		t.Fatalf("读取审计失败：%v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应有 2 条审计，实际 %d", len(entries))
	}
	// 倒序返回：最新一条在前，页面直接渲染不用再排序。
	if entries[0].Action != "签发授权码" {
		t.Fatalf("应按时间倒序，实际首条是 %s", entries[0].Action)
	}
	if entries[1].Actor != "admin" {
		t.Fatalf("审计应记录操作者，实际 %q", entries[1].Actor)
	}
}
