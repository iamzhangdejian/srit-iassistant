package licadmin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 发布管理（管理端，需要登录）与更新通道（客户端，公开）分两组。
//
// 客户端的两个接口刻意不鉴权：桌面安装包里没有账号，也没有可信的密钥；
// 一旦要求令牌，令牌就只能硬编码进安装包，泄漏后等同于对所有客户公开。
// 因此这里靠「回环限制 / 内网部署」兜底，且只暴露版本号、说明、sha256 与
// 发布包本身——不含任何台账或授权信息。

func (s *Server) handleReleaseList(w http.ResponseWriter, r *http.Request) {
	if s.releases == nil {
		writeJSONError(w, http.StatusNotImplemented, "发布管理未启用")
		return
	}
	list, err := s.releases.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": list})
}

func (s *Server) handleReleaseUpload(w http.ResponseWriter, r *http.Request) {
	if s.releases == nil {
		writeJSONError(w, http.StatusNotImplemented, "发布管理未启用")
		return
	}

	// 上限比技能包大得多：安装包本身就有几百 MB。
	r.Body = http.MaxBytesReader(w, r.Body, maxReleaseBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "读取上传内容失败："+err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "没有收到发布包文件（form 字段名 file）")
		return
	}
	defer file.Close()

	data, err := readAllLimited(file, maxReleaseBytes)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	release := Release{
		Platform: r.FormValue("platform"),
		Arch:     r.FormValue("arch"),
		Channel:  r.FormValue("channel"),
		Version:  r.FormValue("version"),
		Notes:    strings.TrimSpace(r.FormValue("notes")),
		Filename: header.Filename,
	}
	saved, err := s.releases.Import(release, data, time.Now())
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrReleaseExists) {
			status = http.StatusConflict
		}
		writeJSONError(w, status, err.Error())
		return
	}
	s.auditAs(s.actor(r), r, "发布上传", saved.Filename,
		describeRelease(saved))
	writeJSON(w, http.StatusOK, map[string]any{"release": saved})
}

func (s *Server) handleReleaseAction(w http.ResponseWriter, r *http.Request) {
	if s.releases == nil {
		writeJSONError(w, http.StatusNotImplemented, "发布管理未启用")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/releases/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSONError(w, http.StatusNotFound, "缺少发布记录 id")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "发布记录 id 不合法")
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "只支持 GET")
			return
		}
		rel, err := s.releases.Get(id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"release": rel})
		return
	}

	// 写操作只认会话：自动化令牌不该有「把某个版本推给全量客户」的能力。
	if user, _, ok := s.sessionUser(r, time.Now()); !ok || user == "" {
		writeJSONError(w, http.StatusUnauthorized, "该操作需要登录管理端")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}

	switch parts[1] {
	case "publish":
		rel, err := s.releases.Publish(id, time.Now())
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auditAs(s.actor(r), r, "发布上线", rel.Filename, describeRelease(rel))
		writeJSON(w, http.StatusOK, map[string]any{"release": rel})
	case "unpublish":
		rel, err := s.releases.Unpublish(id)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auditAs(s.actor(r), r, "发布撤回", rel.Filename, describeRelease(rel))
		writeJSON(w, http.StatusOK, map[string]any{"release": rel})
	case "delete":
		rel, err := s.releases.Get(id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		if err := s.releases.Delete(id); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.auditAs(s.actor(r), r, "发布删除", rel.Filename, describeRelease(rel))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSONError(w, http.StatusNotFound, "未知操作："+parts[1])
	}
}

// handleUpdateLatest 是客户端检查更新用的公开接口。
//
// 请求：GET /api/updates/latest?platform=darwin&arch=arm64&channel=stable&current=0.9.0
// 响应：update_available=true 时带 latest（含下载地址与 sha256），
//
//	没有更新时只带 current 与 latest_version（都是归一化后的值）。
func (s *Server) handleUpdateLatest(w http.ResponseWriter, r *http.Request) {
	if s.releases == nil {
		writeJSONError(w, http.StatusNotImplemented, "更新通道未启用")
		return
	}
	q := r.URL.Query()
	platform := strings.TrimSpace(q.Get("platform"))
	arch := strings.TrimSpace(q.Get("arch"))
	channel := strings.TrimSpace(q.Get("channel"))
	current := normalizeVersion(q.Get("current"))
	if platform == "" || arch == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少 platform 或 arch 参数")
		return
	}

	latest, err := s.releases.Latest(platform, arch, channel)
	if err != nil {
		if errors.Is(err, ErrReleaseNotFound) {
			// 没有已发布版本不是错误：告诉客户端「这条通道还没有发布」。
			writeJSON(w, http.StatusOK, map[string]any{
				"update_available": false,
				"reason":           "该平台/架构还没有已发布版本",
				"current":          current,
			})
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	available := current == "" || compareVersion(latest.Version, current) > 0
	resp := map[string]any{
		"update_available": available,
		"current":          current,
		"latest_version":   latest.Version,
	}
	if available {
		resp["latest"] = map[string]any{
			"version":      latest.Version,
			"notes":        latest.Notes,
			"size_bytes":   latest.SizeBytes,
			"sha256":       latest.SHA256,
			"published_at": latest.PublishedAt,
			"url":          fmt.Sprintf("/api/updates/download/%d", latest.ID),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUpdateDownload 公开下发发布包本体。
//
// 只允许下载「已发布」的记录：草稿状态的包不该被知道 id 的人拿到，
// 否则运营准备的下一版会提前泄漏。
func (s *Server) handleUpdateDownload(w http.ResponseWriter, r *http.Request) {
	if s.releases == nil {
		http.NotFound(w, r)
		return
	}
	idText := strings.TrimPrefix(r.URL.Path, "/api/updates/download/")
	idText = strings.Trim(idText, "/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		http.Error(w, "发布记录 id 不合法", http.StatusBadRequest)
		return
	}
	rel, err := s.releases.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if rel.Status != releaseStatusPublished {
		http.Error(w, "该版本尚未发布", http.StatusNotFound)
		return
	}

	path := s.releases.Path(rel)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rel.Filename))
	w.Header().Set("X-Content-SHA256", rel.SHA256)
	w.Header().Set("X-Release-Version", rel.Version)
	http.ServeFile(w, r, path)
}

func describeRelease(r Release) string {
	return fmt.Sprintf("%s/%s %s v%s（%s，%s）",
		r.Platform, r.Arch, r.Channel, r.Version, r.Status, humanBytes(r.SizeBytes))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// readAllLimited 读满上限即停，避免超大包把内存打爆。
func readAllLimited(r interface{ Read([]byte) (int, error) }, limit int64) ([]byte, error) {
	buf := make([]byte, 0, 1<<20)
	chunk := make([]byte, 1<<20)
	var total int64
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			total += int64(n)
			if total > limit {
				return nil, fmt.Errorf("上传内容超过 %d MB 上限", limit>>20)
			}
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("读取上传内容失败: %w", err)
		}
	}
	return buf, nil
}
