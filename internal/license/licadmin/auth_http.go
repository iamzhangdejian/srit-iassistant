package licadmin

// 认证相关的 HTTP 层：登录、初始化、改密、会话 Cookie、审计查询。

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// subtleCompare 以常量时间比较两个字符串，避免按字节提前返回泄漏口令前缀。
func subtleCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// sessionCookie 写入会话 Cookie。
//
// HttpOnly 是硬要求：管理端页面里没有需要读 Cookie 的脚本，放开只会给
// XSS 留一条直接偷会话的路。SameSite=Strict 让「从别处点链接进来」不带
// Cookie，避免跨站请求伪造。不发 Secure 是因为服务只跑在 http://127.0.0.1。
func (s *Server) sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

// issueSession 签发会话并写 Cookie。
func (s *Server) issueSession(w http.ResponseWriter, now time.Time) error {
	key, err := s.auth.SessionKey(nowCtx())
	if err != nil {
		return err
	}
	tok := SignSession(key, 1, now.Add(sessionTTL))
	http.SetCookie(w, s.sessionCookie(tok, int(sessionTTL.Seconds())))
	return nil
}

// sessionUser 从请求中解析出会话用户。
//
// 返回值 ok=false 表示没有有效会话；renew=true 表示剩余寿命不足一半，
// 调用方应顺手重签 Cookie 实现滑动续期。
func (s *Server) sessionUser(r *http.Request, now time.Time) (user string, renew bool, ok bool) {
	if s.auth == nil {
		return "", false, false
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", false, false
	}
	key, err := s.auth.SessionKey(nowCtx())
	if err != nil {
		return "", false, false
	}
	exp, valid := ParseSession(key, c.Value, now)
	if !valid {
		return "", false, false
	}
	name, err := s.auth.Username(nowCtx())
	if err != nil {
		return "", false, false
	}
	return name, exp.Sub(now) < sessionRenewAfter, true
}

// requestToken 取出请求里携带的自动化令牌。
//
// 头优先、Cookie 兜底：老页面把口令存在 Cookie 里，脚本走头。
func requestToken(r *http.Request) string {
	if got := r.Header.Get("X-Licadmin-Token"); got != "" {
		return got
	}
	if c, err := r.Cookie("licadmin_token"); err == nil {
		return c.Value
	}
	return ""
}

// tokenIdentity 校验自动化令牌并返回它的标签与用途。
//
// 两种令牌：`-token` 静态令牌（视为全用途，给老脚本用的逃生门）与
// 库里按用途签发的令牌（`licenses`/`skills`/`releases`）。
// 自动化令牌的能力被刻意限制在「发码与只读」：它多半被写进脚本或 CI
// 变量里，一旦泄漏也只影响发码，不能让持有者改账号密码——否则等于用
// 一把弱保护的钥匙换走了整个系统的所有权。
func (s *Server) tokenIdentity(r *http.Request) (label string, scopes []string, ok bool) {
	got := requestToken(r)
	if got == "" {
		return "", nil, false
	}
	if s.token != "" && subtleCompare(got, s.token) {
		return "静态令牌", allTokenScopes, true
	}
	if s.tokens == nil {
		return "", nil, false
	}
	tok, err := s.tokens.Lookup(got, time.Now())
	if err != nil {
		return "", nil, false
	}
	return tok.Label, tok.Scopes, true
}

// automationTokenOK 表示「这是一把有效的自动化令牌」，不区分用途。
func (s *Server) automationTokenOK(r *http.Request) bool {
	_, _, ok := s.tokenIdentity(r)
	return ok
}

// actor 返回当前操作者，用于审计。
func (s *Server) actor(r *http.Request) string {
	if name, _, ok := s.sessionUser(r, time.Now()); ok {
		return name
	}
	if label, _, ok := s.tokenIdentity(r); ok {
		if label == "" {
			label = "自动化令牌"
		}
		return "令牌：" + label
	}
	return "未知"
}

// clientIP 取来源 IP（只用于审计，不做信任判断）。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// audit 写一条审计（操作者从请求推断）。
func (s *Server) audit(r *http.Request, action, target, detail string) {
	s.auditAs(s.actor(r), r, action, target, detail)
}

// auditAs 用指定操作者写审计。
//
// 初始化和登录发生在「Cookie 还没回到请求里」的时刻，从请求推断只能得到
// 「未知」；这两处必须显式传操作者，否则审计第一条就丢了身份。
func (s *Server) auditAs(actor string, r *http.Request, action, target, detail string) {
	if s.auth == nil {
		return
	}
	s.auth.AppendAudit(nowCtx(), AuditEntry{
		Actor:  actor,
		Action: action,
		Target: target,
		Detail: detail,
		IP:     clientIP(r),
	})
}

// requireAuth 要求「会话或自动化令牌」。授权相关接口都走这里。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.requireScope("", next)
}

// requireScope 要求「已登录」或「持有带该用途的令牌」。
//
// scope 为空表示只要求是有效令牌（老的 -token 行为）；否则按用途校验，
// 缺用途返回 403 而不是 401——调用方据此能分清「没登录」和「这把钥匙
// 没有这个权限」。
func (s *Server) requireScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		if _, renew, ok := s.sessionUser(r, now); ok {
			if renew {
				_ = s.issueSession(w, now)
			}
			next(w, r)
			return
		}
		if label, scopes, ok := s.tokenIdentity(r); ok {
			if scope == "" || containsString(scopes, scope) {
				next(w, r)
				return
			}
			writeJSONError(w, http.StatusForbidden,
				"令牌「"+label+"」没有「"+scope+"」用途，请让管理员在「系统 → 访问令牌」里重新签发")
			return
		}
		writeJSONError(w, http.StatusUnauthorized, "请先登录")
	}
}

// containsString 判断字符串是否在切片里（用途列表很短，线性扫足够）。
func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// requireSession 要求真实登录会话：账号管理（改密）不接受自动化令牌。
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		if _, renew, ok := s.sessionUser(r, now); ok {
			if renew {
				_ = s.issueSession(w, now)
			}
			next(w, r)
			return
		}
		writeJSONError(w, http.StatusUnauthorized, "请先登录")
	}
}

// handleAuthStatus 报告登录状态，页面启动时第一个调用。
//
// 免认证：页面在登录前必须知道「是否需要初始化」「当前是否已登录」，
// 否则只能先弹一个盲目的登录框。
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	resp := map[string]any{
		"setup_required":  false,
		"authenticated":   false,
		"username":        "",
		"method":          "",
		"auth_enabled":    s.auth != nil,
		"automation_only": s.token != "",
		"server_time":     now.Unix(),
	}
	if s.auth == nil {
		// 未配置账号库（老用法：纯口令模式），让页面退回口令输入。
		writeJSON(w, http.StatusOK, resp)
		return
	}
	setup, err := s.auth.SetupRequired(nowCtx())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取账号状态失败："+err.Error())
		return
	}
	resp["setup_required"] = setup
	if name, _, ok := s.sessionUser(r, now); ok {
		resp["authenticated"] = true
		resp["username"] = name
		resp["method"] = "session"
	} else if s.automationTokenOK(r) {
		resp["authenticated"] = true
		resp["username"] = "自动化令牌"
		resp["method"] = "token"
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAuthSetup 初始化管理员账号（只在没有账号时可用）。
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	if s.auth == nil {
		writeJSONError(w, http.StatusBadRequest, "当前未启用账号登录")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if err := s.auth.CreateAdmin(nowCtx(), body.Username, body.Password); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrAuthExists) {
			status = http.StatusConflict
		}
		writeJSONError(w, status, err.Error())
		return
	}
	name, _ := s.auth.Username(nowCtx())
	if err := s.issueSession(w, time.Now()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建会话失败："+err.Error())
		return
	}
	s.auditAs(name, r, "账号初始化", name, "首次创建管理员账号")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": name})
}

// handleAuthLogin 账号密码登录。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	if s.auth == nil {
		writeJSONError(w, http.StatusBadRequest, "当前未启用账号登录")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	now := time.Now()
	name, err := s.auth.Authenticate(nowCtx(), body.Username, body.Password, now)
	if err != nil {
		var locked *LockedError
		if errors.As(err, &locked) {
			s.auth.AppendAudit(nowCtx(), AuditEntry{
				Actor: strings.TrimSpace(body.Username), Action: "登录被锁定",
				Detail: locked.Error(), IP: clientIP(r),
			})
			w.Header().Set("Retry-After", strconv.Itoa(int(locked.Remain.Seconds())+1))
			writeJSON(w, http.StatusLocked, map[string]any{
				"error":        locked.Error(),
				"locked_until": locked.Until.Unix(),
				"retry_after":  int(locked.Remain.Seconds()) + 1,
			})
			return
		}
		var bad *BadCredentialsError
		if errors.As(err, &bad) {
			s.auth.AppendAudit(nowCtx(), AuditEntry{
				Actor: strings.TrimSpace(body.Username), Action: "登录失败",
				Detail: bad.Error(), IP: clientIP(r),
			})
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":     bad.Error(),
				"remaining": bad.Remaining,
			})
			return
		}
		if errors.Is(err, ErrAuthNotSetup) {
			writeJSONError(w, http.StatusConflict, "尚未初始化管理员账号")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.issueSession(w, now); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建会话失败："+err.Error())
		return
	}
	s.auth.AppendAudit(nowCtx(), AuditEntry{
		Actor: name, Action: "登录成功", IP: clientIP(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": name})
}

// handleAuthLogout 退出登录。
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	if name, _, ok := s.sessionUser(r, time.Now()); ok && s.auth != nil {
		s.auth.AppendAudit(nowCtx(), AuditEntry{Actor: name, Action: "退出登录", IP: clientIP(r)})
	}
	http.SetCookie(w, s.sessionCookie("", -1))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthPassword 修改密码（仅限会话）。
func (s *Server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	name, _ := s.auth.Username(nowCtx())
	now := time.Now()
	if err := s.auth.ChangePassword(nowCtx(), name, body.Current, body.Next, now); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 改密会轮换会话密钥，当前会话必须重签，否则下一次请求就被自己踢掉。
	if err := s.issueSession(w, now); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "重建会话失败："+err.Error())
		return
	}
	s.auth.AppendAudit(nowCtx(), AuditEntry{Actor: name, Action: "修改密码", IP: clientIP(r)})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAudit 返回最近审计记录（仅限会话：审计里有其他会话的操作痕迹）。
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeJSONError(w, http.StatusBadRequest, "当前未启用账号登录")
		return
	}
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	entries, err := s.auth.ListAudit(nowCtx(), limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// nowCtx 返回请求级上下文；后台任务用 context.Background 即可。
func nowCtx() context.Context { return context.Background() }
