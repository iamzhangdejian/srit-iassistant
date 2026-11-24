package licadmin

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// Server 是发码管理端的 HTTP 服务。
type Server struct {
	svc *Service
	// auth 是账号/会话/审计存储。nil 表示退回纯口令模式（老用法）。
	auth *AuthStore
	// token 是自动化令牌。
	//
	// 阶段 2 起浏览器走账号登录（会话 Cookie），这把令牌只留给脚本与 CI：
	// 它被写进 shell 历史或 CI 变量的概率远高于人脑记忆，所以能力被限制
	// 在授权相关接口，不能改账号密码。
	token string
	// mux 是路由表。
	mux *http.ServeMux
	// allowRemote 决定是否允许非回环地址访问。
	allowRemote bool
	// skills 是技能包库（阶段 3）。nil 表示部署形态没带技能管理。
	skills *SkillStore
	// releases 是版本发布库（阶段 4）。nil 表示不带更新通道。
	releases *ReleaseStore
	// tokens 是按用途签发的自动化令牌（阶段 4）。nil 时只认 -token。
	tokens *TokenStore
	// logPath 是日志文件路径，供「系统 → 运行日志」读取。
	logPath string
	// dataDir 是数据目录（备份/恢复/日志都以它为根）。
	dataDir string
}

// ServerOptions 是构造函数参数。
type ServerOptions struct {
	Service *Service
	// Auth 是账号存储；为 nil 时退回纯口令模式（仅用于测试与过渡）。
	Auth *AuthStore
	// Token 是自动化令牌。为空表示脚本接口不开放。
	Token string
	// AllowRemote 允许局域网访问。
	AllowRemote bool
	// Skills 是技能库；为 nil 时不注册技能接口。
	Skills *SkillStore
	// Releases 是发布库；为 nil 时不注册发布与更新接口。
	Releases *ReleaseStore
	// Tokens 是按用途签发的令牌库；为 nil 时不注册令牌管理接口。
	Tokens *TokenStore
	// LogPath 是日志文件路径（-app-mode 下为 ~/Library/Logs/…）。
	LogPath string
	// DataDir 是数据目录；为空时由 Auth 的 DB 路径推断。
	DataDir string
}

// NewServer 构造服务并注册路由。
func NewServer(opts ServerOptions) *Server {
	s := &Server{
		svc:         opts.Service,
		auth:        opts.Auth,
		token:       opts.Token,
		mux:         http.NewServeMux(),
		allowRemote: opts.AllowRemote,
		skills:      opts.Skills,
		releases:    opts.Releases,
		tokens:      opts.Tokens,
		logPath:     opts.LogPath,
		dataDir:     opts.DataDir,
	}
	if s.dataDir == "" && opts.Auth != nil {
		s.dataDir = opts.Auth.DataDir()
	}
	s.routes()
	return s
}

// routes 注册所有接口。
func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/assets/", s.handleAssets)
	s.mux.HandleFunc("/api/health", s.handleHealth)
	// 认证接口：status 免鉴权（页面启动先问状态），其余按语义各自加闸。
	s.mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("/api/auth/setup", s.handleAuthSetup)
	s.mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	s.mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	s.mux.HandleFunc("/api/auth/password", s.requireSession(s.handleAuthPassword))
	s.mux.HandleFunc("/api/audit", s.requireSession(s.handleAudit))
	// /api/meta 故意不鉴权：它只含公钥与功能清单，没有任何秘密，而页面
	// 必须在用户输入口令之前就拿到功能清单才能渲染勾选框。若要求认证，
	// 首屏会出现「无勾选框、公钥空白」，用户根本不知道该选什么。
	s.mux.HandleFunc("/api/meta", s.handleMeta)
	s.mux.HandleFunc("/api/issue", s.requireScope(ScopeLicenses, s.handleIssue))
	s.mux.HandleFunc("/api/issue/batch", s.requireScope(ScopeLicenses, s.handleIssueBatch))
	s.mux.HandleFunc("/api/entries", s.requireScope(ScopeLicenses, s.handleEntries))
	s.mux.HandleFunc("/api/entries/", s.requireScope(ScopeLicenses, s.handleEntryAction))
	s.mux.HandleFunc("/api/stats", s.requireScope(ScopeLicenses, s.handleStats))
	s.mux.HandleFunc("/api/inspect", s.requireScope(ScopeLicenses, s.handleInspect))
	s.mux.HandleFunc("/api/export", s.requireScope(ScopeLicenses, s.handleExport))
	// 技能管理（阶段 3）：读与校验对令牌开放（CI 也能查），
	// 上传/发布/删除需要账号会话 —— 它们决定客户机器上装什么。
	if s.skills != nil {
		s.mux.HandleFunc("/api/skills", s.requireScope(ScopeSkills, s.handleSkillList))
		s.mux.HandleFunc("/api/skills/validate", s.requireScope(ScopeSkills, s.handleSkillValidate))
		s.mux.HandleFunc("/api/skills/upload", s.requireSession(s.handleSkillUpload))
		s.mux.HandleFunc("/api/skills/bundle", s.requireScope(ScopeSkills, s.handleSkillBundle))
		s.mux.HandleFunc("/api/skills/", s.requireScope(ScopeSkills, s.handleSkillAction))
	}

	if s.releases != nil {
		s.mux.HandleFunc("/api/releases", s.requireScope(ScopeReleases, s.handleReleaseList))
		s.mux.HandleFunc("/api/releases/upload", s.requireSession(s.handleReleaseUpload))
		s.mux.HandleFunc("/api/releases/", s.requireScope(ScopeReleases, s.handleReleaseAction))
		// 客户端更新通道：公开，不含任何台账/授权信息。
		s.mux.HandleFunc("/api/updates/latest", s.handleUpdateLatest)
		s.mux.HandleFunc("/api/updates/download/", s.handleUpdateDownload)
	}

	// 系统页（阶段 4）：备份/恢复/日志只认会话——它们能拿到私钥与数据库。
	s.mux.HandleFunc("/api/system/status", s.requireSession(s.handleSystemStatus))
	s.mux.HandleFunc("/api/system/backup", s.requireSession(s.handleSystemBackup))
	s.mux.HandleFunc("/api/system/restore", s.requireSession(s.handleSystemRestore))
	s.mux.HandleFunc("/api/system/logs", s.requireSession(s.handleSystemLogs))

	// 令牌管理：同样只认会话，否则拿到一把令牌就能给自己再签一把。
	if s.tokens != nil {
		s.mux.HandleFunc("/api/tokens", s.requireSession(s.handleTokens))
		s.mux.HandleFunc("/api/tokens/", s.requireSession(s.handleTokenAction))
	}
}

// Handler 返回可直接交给 http.Server 的处理器。
//
// 这里再包一层来源校验而不是只在 ListenAndServe 时绑 127.0.0.1：
// 万一部署时用了反向代理或监听 0.0.0.0，来源校验是最后一道闸。
// 发码端泄露等于授权体系失守，值得多一道。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowRemote && !isLoopbackRequest(r) {
			http.Error(w, "该服务仅允许本机访问", http.StatusForbidden)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

// isLoopbackRequest 判断请求来源是否为本机。
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleHealth 是免认证的健康检查，便于部署时探测。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMeta 返回页面初始化需要的信息。
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		// 公钥要显示在页面上：部署客户端时要把它写进代码，
		// 摆在这里能避免「换密钥忘了同步客户端」的经典事故。
		"public_key":    s.svc.PublicKeyBase64(),
		// key_id 是这把公钥的指纹。它比整段 base64 好核对：客户端授权页
		// 现在也会显示自己的 key_id，两边一对就知道是不是同一把密钥。
		"key_id":        s.svc.PublicKeyID(),
		"features":      license.AllFeatures,
		"server_time":   time.Now().Unix(),
		"require_auth":  s.auth != nil || s.token != "",
		"auth_enabled":  s.auth != nil,
		"token_enabled": s.token != "",
	})
}

// issueRequestDTO 是签发接口的请求体。
type issueRequestDTO struct {
	MACs     macList  `json:"macs"`
	Days     int      `json:"days"`
	User     string   `json:"user"`
	Org      string   `json:"org"`
	Features []string `json:"features"`
	Serial   string   `json:"serial"`
	Note     string   `json:"note"`
	Force    bool     `json:"force"`
	// Reset 签发复位码：客户端导入时丢弃历史到期水位。
	//
	// 界面上是默认折叠的高级开关，正常发码永远不该勾它——勾错了等于
	// 主动削弱那台机器的回滚防护。
	Reset bool `json:"reset"`
}

// macList 让 macs 同时接受字符串与数组两种写法。
//
// 界面里是 textarea（多行字符串），而脚本/CI 习惯传数组；只认一种，
// 另一种就会收到「请求体不是合法 JSON」——这个报错还指向了错误的原因，
// 排查成本极高。这里两种都收，最后统一成「分隔符拼接的字符串」。
type macList string

func (m *macList) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*m = ""
		return nil
	}
	if data[0] == '[' {
		var list []string
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*m = macList(strings.Join(list, ","))
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err != nil {
		return err
	}
	*m = macList(one)
	return nil
}

// toDomain 把 DTO 转成领域请求。
func (d issueRequestDTO) toDomain() IssueRequest {
	// MAC 支持逗号、换行、空格、分号任意混用：用户从各种地方复制过来。
	raw := strings.FieldsFunc(string(d.MACs), func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == ';' || r == '\t'
	})
	feats := make([]license.Feature, 0, len(d.Features))
	for _, f := range d.Features {
		feats = append(feats, license.Feature(f))
	}
	return IssueRequest{
		MACs:     raw,
		Days:     d.Days,
		User:     d.User,
		Org:      d.Org,
		Features: feats,
		Serial:   d.Serial,
		Note:     d.Note,
		Force:    d.Force,
		Reset:    d.Reset,
	}
}

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var dto issueRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	res, err := s.svc.Issue(dto.toDomain())
	if err == nil {
		s.audit(r, "签发授权码", res.Entry.Serial, describeIssue(&res))
	}
	if err != nil {
		// 这里的错误几乎都是「用户输入有问题」而不是服务器故障，
		// 所以用 400 而不是 500，并把原文回显给他看。
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// batchRequestDTO 是批量签发的请求体。
type batchRequestDTO struct {
	Text     string   `json:"text"`
	Days     int      `json:"days"`
	Features []string `json:"features"`
	Org      string   `json:"org"`
}

func (s *Server) handleIssueBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var dto batchRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if strings.TrimSpace(dto.Text) == "" {
		writeJSONError(w, http.StatusBadRequest, "请粘贴要签发的数据")
		return
	}
	feats := make([]license.Feature, 0, len(dto.Features))
	for _, f := range dto.Features {
		feats = append(feats, license.Feature(f))
	}
	res := s.svc.IssueBatch(dto.Text, IssueRequest{
		Days:     dto.Days,
		Features: feats,
		Org:      dto.Org,
	})
	s.audit(r, "批量签发", "", fmt.Sprintf("成功 %d 条，失败 %d 条", res.Issued, res.Failed))
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleEntries(w http.ResponseWriter, r *http.Request) {
	q := Query{
		Keyword: r.URL.Query().Get("keyword"),
		Status:  Status(r.URL.Query().Get("status")),
	}
	if limit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		q.Limit = limit
	}
	if offset, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && offset > 0 {
		q.Offset = offset
	}
	entries, total := s.svc.store.ListPage(q)
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
		"limit":   q.Limit,
		"offset":  q.Offset,
	})
}

// handleEntryAction 处理 /api/entries/{id} 与其下的动作。
//
//	GET  /api/entries/{id}        取单条详情（含完整授权码，便于客服复制）
//	POST /api/entries/{id}/revoke 作废（body: {reason, note}）
//	POST /api/entries/{id}/renew  续期（body: {days, note}），返回新码
func (s *Server) handleEntryAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/entries/")
	idStr, action, _ := strings.Cut(path, "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "记录 ID 不合法")
		return
	}

	// 详情：路径里没有动作段。
	if action == "" {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "只支持 GET")
			return
		}
		entry, ok := s.svc.store.Get(id)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "记录不存在")
			return
		}
		writeJSON(w, http.StatusOK, entry)
		return
	}

	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}

	switch action {
	case "revoke":
		var body struct {
			Reason string `json:"reason"`
			Note   string `json:"note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		entry, err := s.svc.store.RevokeWithReason(id, strings.TrimSpace(body.Reason), strings.TrimSpace(body.Note), time.Now())
		if err == nil {
			s.audit(r, "作废授权码", entry.Serial, strings.TrimSpace(body.Reason))
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, entry)
	case "renew":
		var body struct {
			Days int    `json:"days"`
			Note string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON")
			return
		}
		res, err := s.svc.Renew(id, RenewRequest{Days: body.Days, Note: body.Note})
		if err == nil {
			s.audit(r, "续期授权码", res.Entry.Serial, fmt.Sprintf("原记录 #%d，续 %d 天", id, body.Days))
		}
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.store.Stats(time.Now()))
}

func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeJSONError(w, http.StatusBadRequest, "请粘贴要校验的授权码")
		return
	}
	writeJSON(w, http.StatusOK, s.svc.Inspect(body.Code))
}

// handleExport 导出台账 CSV。
//
// 用 CSV 而不是 JSON：拿到台账的人多半是要在 Excel 里做筛选与统计，
// 或者发给财务核对合同。
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	q := Query{
		Keyword: r.URL.Query().Get("keyword"),
		Status:  Status(r.URL.Query().Get("status")),
	}
	entries := s.svc.store.List(q)
	s.audit(r, "导出台账", "", fmt.Sprintf("%d 条", len(entries)))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"license-ledger-%s.csv\"", time.Now().Format("20060102-1504")))
	// BOM 让 Excel 正确识别 UTF-8，否则中文列名会乱码。
	fmt.Fprint(w, "\ufeff")
	fmt.Fprintln(w, "序列号,绑定MAC,机器指纹,使用人,单位,功能,签发时间,到期时间,状态,续期自,作废原因,备注,授权码")
	for _, e := range entries {
		parent := ""
		if e.ParentID > 0 {
			parent = "#" + strconv.FormatInt(e.ParentID, 10)
		}
		fmt.Fprintf(w, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
			csvCell(e.Serial),
			csvCell(strings.Join(e.MACs, " ")),
			csvCell(e.Fingerprint),
			csvCell(e.User),
			csvCell(e.Org),
			csvCell(joinFeatures(e.Features)),
			csvCell(time.Unix(e.IssuedAt, 0).Format("2006-01-02 15:04")),
			csvCell(e.ExpiresText()),
			csvCell(statusText(e.Status)),
			csvCell(parent),
			csvCell(e.RevokeReason),
			csvCell(e.Note),
			csvCell(e.Code),
		)
	}
}

// describeIssue 生成审计里的一句话描述。
func describeIssue(res *IssueResult) string {
	parts := make([]string, 0, 3)
	if res.Entry.User != "" || res.Entry.Org != "" {
		parts = append(parts, strings.TrimSpace(res.Entry.User+" / "+res.Entry.Org))
	}
	if n := len(res.Entry.Features); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 项功能", n))
	} else {
		parts = append(parts, "全部功能")
	}
	parts = append(parts, "到期 "+res.Entry.ExpiresText())
	return strings.Join(parts, "，")
}

func joinFeatures(fs []license.Feature) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = string(f)
	}
	return strings.Join(parts, " ")
}

func statusText(s Status) string {
	if s == StatusRevoked {
		return "已作废"
	}
	return "有效"
}

// csvCell 做最小必要的转义：含逗号、引号、换行时用双引号包裹。
func csvCell(v string) string {
	if strings.ContainsAny(v, ",\"\n\r") {
		return "\"" + strings.ReplaceAll(v, "\"", "\"\"") + "\""
	}
	return v
}

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError 输出统一形状的错误。
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
