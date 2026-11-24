package licadmin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 系统页接口：备份、恢复、令牌、日志、运行状态。
//
// 全部只认会话：这些接口能拿到私钥、能改数据库、能看日志，任何一把
// 自动化令牌都不该具备这种能力——令牌是给流水线发码用的，不是给运维
// 做数据恢复用的。

// handleSystemStatus 汇总数据目录里的关键文件，供界面显示「数据在哪、多大」。
func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "请使用 GET")
		return
	}
	dir := s.dataDir

	type fileInfo struct {
		Name      string `json:"name"`
		Exists    bool   `json:"exists"`
		SizeBytes int64  `json:"size_bytes"`
		ModTime   int64  `json:"mod_time"`
	}
	var files []fileInfo
	for _, name := range []string{"licadmin.db", "ledger.json", "private.key", "public.key", "自动化令牌.txt", "访问口令.txt"} {
		fi := fileInfo{Name: name}
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
			fi.Exists = true
			fi.SizeBytes = st.Size()
			fi.ModTime = st.ModTime().Unix()
		}
		files = append(files, fi)
	}

	// 技能包与发行包按目录统计（大文件目录，逐个文件列出来没有意义）
	dirStats := func(sub string) (int, int64) {
		var count int
		var size int64
		_ = filepath.Walk(filepath.Join(dir, sub), func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			count++
			size += info.Size()
			return nil
		})
		return count, size
	}
	skillFiles, skillBytes := dirStats("skills")
	releaseFiles, releaseBytes := dirStats("releases")

	pending, stagedAt, stagedNames := PendingRestore(dir)

	writeJSON(w, http.StatusOK, map[string]any{
		"data_dir":        dir,
		"files":           files,
		"skills":          map[string]any{"files": skillFiles, "size_bytes": skillBytes},
		"releases":        map[string]any{"files": releaseFiles, "size_bytes": releaseBytes},
		"pending_restore": pending,
		"staged_at":       stagedAt,
		"staged_entries":  stagedNames,
		"token_enabled":   s.token != "",
		"scopes":          allTokenScopes,
		"now":             time.Now().Unix(),
	})
}

// handleSystemBackup 生成并下载备份 zip。
func (s *Server) handleSystemBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "请使用 GET")
		return
	}
	includeReleases := r.URL.Query().Get("include") == "releases"
	now := time.Now()
	name := fmt.Sprintf("licadmin-backup-%s.zip", now.Format("20060102-150405"))
	outPath := filepath.Join(os.TempDir(), name)

	info, err := BuildBackup(s.auth.DB(), s.dataDir, outPath, includeReleases, now)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "生成备份失败："+err.Error())
		return
	}
	s.audit(r, "system.backup", "data", fmt.Sprintf("生成备份 %s（%d 字节，%d 个条目，含发行包=%v）",
		name, info.SizeBytes, len(info.Entries), includeReleases))

	f, err := os.Open(outPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取备份失败："+err.Error())
		return
	}
	defer func() {
		f.Close()
		_ = os.Remove(outPath) // 备份文件只在内存/磁盘上短暂存在，下载完即删
	}()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Content-Length", strconv.FormatInt(info.SizeBytes, 10))
	w.Header().Set("X-Backup-Entries", strconv.Itoa(len(info.Entries)))
	_, _ = io.Copy(w, f)
}

// handleSystemRestore 接收恢复包，落到 `.pending-restore/` 等下次启动生效。
func (s *Server) handleSystemRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "请使用 POST")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRestoreZipBytes))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "恢复包过大或读取失败")
		return
	}
	if len(body) == 0 {
		writeJSONError(w, http.StatusBadRequest, "没有收到文件")
		return
	}

	now := time.Now()
	entries, err := StageRestore(s.dataDir, body, now)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "system.restore.stage", "data", "已暂存恢复包，等待重启生效")

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"staged":  strings.Split(entries, "\n"),
		"message": "恢复包已暂存，重启管理端后生效（启动时会先把现有数据挪到 backup-before-restore-<时间>/）",
	})
}

// handleSystemLogs 返回日志尾部若干行。
func (s *Server) handleSystemLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "请使用 GET")
		return
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			lines = n
		}
	}
	tail, size, err := LogTail(s.logPath, lines)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取日志失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":       s.logPath,
		"exists":     size > 0,
		"size_bytes": size,
		"lines":      tail,
	})
}

// handleTokenList 列出未撤销的令牌。
// handleTokens 是 /api/tokens 的方法分发：GET 列列表、POST 建令牌。
// 单条路由分发而不是分两条注册，是因为两者共用同一路径与同一道会话闸门。
func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleTokenList(w, r)
	case http.MethodPost:
		s.handleTokenCreate(w, r)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "请使用 GET 或 POST")
	}
}

func (s *Server) handleTokenList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "请使用 GET")
		return
	}
	if s.tokens == nil {
		writeJSONError(w, http.StatusNotFound, "令牌管理未启用")
		return
	}
	list, err := s.tokens.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取令牌失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tokens": list,
		"scopes": allTokenScopes,
		// 静态令牌（-token）不落库，这里显式标出来，免得运维以为界面里
		// 没看到它就等于没有令牌可用。
		"static_token_enabled": s.token != "",
	})
}

type createTokenRequest struct {
	Label  string   `json:"label"`
	Scopes []string `json:"scopes"`
}

// handleTokenCreate 新建令牌，明文只返回这一次。
func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "请使用 POST")
		return
	}
	if s.tokens == nil {
		writeJSONError(w, http.StatusNotFound, "令牌管理未启用")
		return
	}
	var req createTokenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	tok, plain, err := s.tokens.Create(req.Label, req.Scopes, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, ErrTokenLabelRequired), errors.Is(err, ErrTokenScopeInvalid):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		default:
			writeJSONError(w, http.StatusInternalServerError, "创建令牌失败："+err.Error())
		}
		return
	}
	s.audit(r, "token.create", fmt.Sprintf("token:%d", tok.ID),
		fmt.Sprintf("新建令牌「%s」（%s）", tok.Label, strings.Join(tok.Scopes, ",")))

	writeJSON(w, http.StatusOK, map[string]any{
		"token":       tok,
		"plain_token": plain,
		"message":     "请立即复制保存：明文只显示这一次",
	})
}

// handleTokenAction 撤销令牌。
func (s *Server) handleTokenAction(w http.ResponseWriter, r *http.Request) {
	if s.tokens == nil {
		writeJSONError(w, http.StatusNotFound, "令牌管理未启用")
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/tokens/")
	idStr = strings.Trim(idStr, "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "令牌 ID 不合法")
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "请使用 DELETE")
		return
	}
	if err := s.tokens.Revoke(id, time.Now()); err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "撤销失败："+err.Error())
		return
	}
	s.audit(r, "token.revoke", fmt.Sprintf("token:%d", id), "撤销令牌")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
