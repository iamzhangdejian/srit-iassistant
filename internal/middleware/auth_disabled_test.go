package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 交付形态的账号密码认证面整组关闭：这些路径即使被（静态资源 catch-all 之类）
// 兜到，也必须是「接口不存在」，而不是被解释成 401 未登录或 LICENSE_MISSING。
func TestDisabledPasswordAuthReturnsNotFound(t *testing.T) {
	if !PasswordAuthDisabled {
		t.Skip("password auth is enabled in this build")
	}

	gin.SetMode(gin.TestMode)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/register"},
		{http.MethodPost, "/api/v1/auth/register-by-invite"},
		{http.MethodPost, "/api/v1/auth/invitations/lookup"},
		{http.MethodPost, "/api/v1/auth/change-password"},
		{http.MethodGet, "/api/v1/auth/oidc/config"},
		{http.MethodGet, "/api/v1/auth/oidc/url"},
		{http.MethodGet, "/api/v1/auth/oidc/start"},
		{http.MethodGet, "/api/v1/auth/oidc/callback"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := gin.New()
			// 用 nil 依赖即可：关闭判定发生在任何服务调用之前。
			r.Use(Auth(nil, nil, nil, nil, nil))
			r.Handle(tc.method, tc.path, func(c *gin.Context) {
				t.Fatalf("handler for %s %s must not run", tc.method, tc.path)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			r.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusNotFound, w.Body.String())
			}
			if !containsString(w.Body.String(), "AUTH_ENDPOINT_DISABLED") {
				t.Fatalf("body should carry AUTH_ENDPOINT_DISABLED, got: %s", w.Body.String())
			}
		})
	}
}

// 保留的认证接口不能被这道闸门误伤：/auth/config 仍然匿名可读，
// 且它回报 password_login_enabled=false，两者语义必须一致。
func TestKeptAuthEndpointsAreNotBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(Auth(nil, nil, nil, nil, nil))
	r.GET("/api/v1/auth/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}