package licadmin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// newTestHTTPServer 起一个带账号库的测试服务。
func newTestHTTPServer(t *testing.T, token string) (*Server, *Service) {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "private.key")
	writeTestKey(t, keyPath)
	store := NewStore(filepath.Join(dir, "ledger.json"))
	svc, err := NewService(ServiceOptions{Store: store, PrivateKeyPath: keyPath})
	if err != nil {
		t.Fatalf("构造服务失败：%v", err)
	}
	auth, err := OpenAuthStore(filepath.Join(dir, "licadmin.db"))
	if err != nil {
		t.Fatalf("打开账号库失败：%v", err)
	}
	t.Cleanup(func() { _ = auth.Close() })
	return NewServer(ServerOptions{Service: svc, Auth: auth, Token: token}), svc
}

// do 发一个请求，可带 Cookie 与令牌头。
func do(t *testing.T, h http.Handler, method, path, body string, hdr map[string]string) *http.Response {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	// Handler 会拒绝非回环来源，测试里显式伪造本机地址。
	r.RemoteAddr = "127.0.0.1:54321"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Result()
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	return out
}

func sessionCookie(resp *http.Response) string {
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

func TestAuthHTTPSetupThenLogin(t *testing.T) {
	srv, _ := newTestHTTPServer(t, "")
	h := srv.Handler()

	st := decode(t, do(t, h, http.MethodGet, "/api/auth/status", "", nil))
	if st["setup_required"] != true {
		t.Fatalf("空库应提示需要初始化，实际 %v", st)
	}

	// 未登录不能访问业务接口。
	if resp := do(t, h, http.MethodGet, "/api/stats", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录访问 /api/stats 应 401，实际 %d", resp.StatusCode)
	}

	// 弱密码被拒。
	if resp := do(t, h, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"short"}`, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("弱密码应 400，实际 %d", resp.StatusCode)
	}

	resp := do(t, h, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"LicAdmin2026"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("初始化应成功，实际 %d", resp.StatusCode)
	}
	cookie := sessionCookie(resp)
	if cookie == "" {
		t.Fatal("初始化成功后应下发会话 Cookie")
	}

	// 带会话可以访问业务接口。
	if resp := do(t, h, http.MethodGet, "/api/stats", "", map[string]string{"Cookie": sessionCookieName + "=" + cookie}); resp.StatusCode != http.StatusOK {
		t.Fatalf("带会话访问 /api/stats 应 200，实际 %d", resp.StatusCode)
	}

	// 退出后会话失效：删 Cookie 等价于换浏览器，这里验证「未带 Cookie 401」。
	if resp := do(t, h, http.MethodPost, "/api/auth/logout", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("退出应 200，实际 %d", resp.StatusCode)
	}

	// 再初始化一次应冲突。
	if resp := do(t, h, http.MethodPost, "/api/auth/setup", `{"username":"x","password":"LicAdmin2026"}`, nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("重复初始化应 409，实际 %d", resp.StatusCode)
	}

	// 正确密码可再次登录。
	resp = do(t, h, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"LicAdmin2026"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录应成功，实际 %d", resp.StatusCode)
	}
	if sessionCookie(resp) == "" {
		t.Fatal("登录成功应下发会话 Cookie")
	}
}

func TestAuthHTTPLockoutReturns423(t *testing.T) {
	srv, _ := newTestHTTPServer(t, "")
	h := srv.Handler()
	if resp := do(t, h, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"LicAdmin2026"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("初始化失败：%d", resp.StatusCode)
	}
	for i := 1; i < freeAttempts; i++ {
		resp := do(t, h, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"bad-pass"}`, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应 401，实际 %d", i, resp.StatusCode)
		}
		body := decode(t, resp)
		if body["remaining"] == nil {
			t.Fatalf("第 %d 次应返回剩余次数", i)
		}
	}
	resp := do(t, h, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"bad-pass"}`, nil)
	if resp.StatusCode != http.StatusLocked {
		t.Fatalf("达到阈值应 423，实际 %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("锁定时应返回 Retry-After 头")
	}
	// 锁定期间正确密码同样 423。
	if resp := do(t, h, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"LicAdmin2026"}`, nil); resp.StatusCode != http.StatusLocked {
		t.Fatalf("锁定期内正确密码也应 423，实际 %d", resp.StatusCode)
	}
}

func TestAutomationTokenScopeIsLimited(t *testing.T) {
	const token = "automation-token-for-test"
	srv, svc := newTestHTTPServer(t, token)
	h := srv.Handler()
	if resp := do(t, h, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"LicAdmin2026"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("初始化失败：%d", resp.StatusCode)
	}

	hdr := map[string]string{"X-Licadmin-Token": token}
	// 授权接口：令牌可用（脚本发码的主路径）。
	if resp := do(t, h, http.MethodGet, "/api/stats", "", hdr); resp.StatusCode != http.StatusOK {
		t.Fatalf("令牌访问 /api/stats 应 200，实际 %d", resp.StatusCode)
	}
	resp := do(t, h, http.MethodPost, "/api/issue",
		`{"macs":"3c:22:fb:00:00:05","days":30,"user":"脚本"}`,
		map[string]string{"X-Licadmin-Token": token, "Content-Type": "application/json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("令牌发码应 200，实际 %d", resp.StatusCode)
	}
	entries := svc.store.List(Query{})
	if len(entries) != 1 {
		t.Fatalf("应签发 1 张码，实际 %d", len(entries))
	}

	// 账号管理：令牌不可用（否则令牌泄漏等于账号被接管）。
	for _, path := range []string{"/api/auth/password", "/api/audit"} {
		if resp := do(t, h, http.MethodPost, path, `{"current":"x","next":"LicAdmin2027"}`, hdr); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("令牌访问 %s 应 401，实际 %d", path, resp.StatusCode)
		}
	}

	// 令牌也不能用来初始化第二个账号（初始化后本来是 409，这里验的是闸门顺序）。
	if resp := do(t, h, http.MethodPost, "/api/auth/setup", `{"username":"x","password":"LicAdmin2026"}`, hdr); resp.StatusCode != http.StatusConflict {
		t.Fatalf("已初始化后 setup 应 409，实际 %d", resp.StatusCode)
	}
}

func TestWrongAutomationTokenRejected(t *testing.T) {
	srv, _ := newTestHTTPServer(t, "right-token")
	h := srv.Handler()
	if resp := do(t, h, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"LicAdmin2026"}`, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("初始化失败：%d", resp.StatusCode)
	}
	if resp := do(t, h, http.MethodGet, "/api/stats", "", map[string]string{"X-Licadmin-Token": "wrong"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误令牌应 401，实际 %d", resp.StatusCode)
	}
}
