package handler

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/hostdir"
)

// 项目目录浏览的边界，与产物面板的用途匹配：
//   - 只列文件，不递归（递归会把 node_modules 拖进来）；
//   - 目录本身仍返回，便于前端展示但不可下载；
//   - 单文件超过上限仍列出，只是标记不可下载，避免前端为止损而整页空白。
const (
	projectTreeMaxEntries = 5000
	projectTreeMaxDepth   = 6
	projectTreeMaxBytes   = 200 << 20
	// 这些目录名一律跳过：它们是构建产物或版本库元数据，既不是用户文件，
	// 数量又经常上万，列出来只会把有用的文件挤掉。
	projectTreeSkipDirs = ".git,node_modules,.venv,venv,__pycache__,.next,dist,build,.cache,.idea,.DS_Store"
)

// projectTreeEntry 是列表的一行。
type projectTreeEntry struct {
	// Path 是绝对路径，前端要拿它做下载与预览的参数。
	Path string `json:"path"`
	// Rel 是相对项目根目录的路径，UI 显示它比显示绝对路径短得多。
	Rel string `json:"rel"`
	// Name 是文件名本身，用于图标与排序。
	Name string `json:"name"`
	// Kind 取 "file" 或 "dir"。
	Kind string `json:"kind"`
	// Depth 是相对根目录的层级（根目录的直接子项为 1）。
	// 前端按它做缩进，不必再从 Rel 里数斜杠。
	Depth int `json:"depth"`
	// Size 对目录为 0。
	Size int64 `json:"size"`
	// ModTime 是 RFC3339 字符串，前端直接格式化。
	ModTime string `json:"mod_time"`
	// Downloadable 为 false 时前端置灰。目录、软链接、超限文件都属于这种。
	Downloadable bool `json:"downloadable"`
	// Truncated 表示还有更多同层条目没列出来。
	Truncated bool `json:"truncated,omitempty"`
}

type projectTreeResponse struct {
	Root      string             `json:"root"`
	Entries   []projectTreeEntry `json:"entries"`
	Total     int                `json:"total"`
	Truncated bool               `json:"truncated"`
}

// ListHostProjectFiles 列出某个已授权项目目录下的文件，供对话页的
// 「项目文件」面板使用。
//
// 安全规则与 reveal 一致：dir 必须精确等于用户通过系统目录选择框授权过的
// 目录，否则 403。这里不做「子目录也算授权」的放宽——用户选的是根，
// 越界访问必须走选择框重新授权。
//
// 只读：本接口不创建、不修改、不删除任何文件。
func (*SystemHandler) ListHostProjectFiles(c *gin.Context) {
	list, _ := currentHostProjectDirStore()
	if list == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"message": "host project directories are not available"},
		})
		return
	}

	dir := strings.TrimSpace(c.Query("dir"))
	approved, ok := hostdir.MatchApproved(dir, list())
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error":   gin.H{"message": "目录未获得访问授权，请通过系统目录选择框选择"},
		})
		return
	}

	info, err := os.Stat(approved)
	if err != nil || !info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"message": "目录不存在或不可访问"},
		})
		return
	}
	// 授权列表里可能留着已删除的目录，这种直接回空列表比报错更有用。

	skip := map[string]struct{}{}
	for _, name := range strings.Split(projectTreeSkipDirs, ",") {
		if name = strings.TrimSpace(name); name != "" {
			skip[name] = struct{}{}
		}
	}

	entries := make([]projectTreeEntry, 0, 128)
	truncated := false
	walkProjectTree(approved, approved, 0, skip, &entries, &truncated)

	sort.SliceStable(entries, func(i, j int) bool {
		// 目录在前，其余按修改时间倒序：用户找的是「刚生成的那份文件」。
		if (entries[i].Kind == "dir") != (entries[j].Kind == "dir") {
			return entries[i].Kind == "dir"
		}
		return entries[i].ModTime > entries[j].ModTime
	})

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": projectTreeResponse{
			Root:      approved,
			Entries:   entries,
			Total:     len(entries),
			Truncated: truncated,
		},
	})
}

// walkProjectTree 遍历目录，收集文件与子目录。
//
// depth 是相对根的层级；超过 projectTreeMaxDepth 就不再深入，
// 因为再往下的内容通常是构建产物而不是用户文件。
func walkProjectTree(
	root, current string,
	depth int,
	skip map[string]struct{},
	entries *[]projectTreeEntry,
	truncated *bool,
) {
	if len(*entries) >= projectTreeMaxEntries {
		*truncated = true
		return
	}
	items, err := os.ReadDir(current)
	if err != nil {
		// 单个目录读不了（权限、竞态删除）不该让整页失败。
		return
	}
	for _, item := range items {
		if len(*entries) >= projectTreeMaxEntries {
			*truncated = true
			return
		}
		name := item.Name()
		if _, isSkip := skip[name]; isSkip {
			continue
		}
		// 隐藏文件对用户意义不大，只跳过点开头的条目（.weknora 也在其中）。
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(current, name)
		info, err := item.Info()
		if err != nil {
			continue
		}

		rel, relErr := filepath.Rel(root, full)
		if relErr != nil {
			rel = name
		}
		rel = filepath.ToSlash(rel)

		if item.IsDir() {
			*entries = append(*entries, projectTreeEntry{
				Path:    full,
				Rel:     rel,
				Name:    name,
				Kind:    "dir",
				Depth:   depth + 1,
				ModTime: info.ModTime().Format(time.RFC3339),
			})
			if depth+1 < projectTreeMaxDepth {
				walkProjectTree(root, full, depth+1, skip, entries, truncated)
			}
			continue
		}
		if !item.Type().IsRegular() {
			// 软链接、设备文件不给下载入口：跟随链接可能读到项目外的东西。
			*entries = append(*entries, projectTreeEntry{
				Path:    full,
				Rel:     rel,
				Name:    name,
				Kind:    "file",
				Depth:   depth + 1,
				Size:    info.Size(),
				ModTime: info.ModTime().Format(time.RFC3339),
			})
			continue
		}
		*entries = append(*entries, projectTreeEntry{
			Path:         full,
			Rel:          rel,
			Name:         name,
			Kind:         "file",
			Depth:        depth + 1,
			Size:         info.Size(),
			ModTime:      info.ModTime().Format(time.RFC3339),
			Downloadable: info.Size() <= projectTreeMaxBytes,
		})
	}
}

type projectFileRequest struct {
	Dir  string `json:"dir" binding:"required"`
	Path string `json:"path" binding:"required"`
}

// resolveApprovedFile 把请求里的 (dir, path) 解析成一个位于已授权目录内的
// 真实文件路径。
//
// 三道校验缺一不可：
//  1. dir 必须在授权列表里；
//  2. path 的 Clean 结果必须仍以 dir 为前缀（挡住 ../ 越界）；
//  3. 解析软链接之后的真实路径也必须在前缀内（挡住指向外部的链接）。
func resolveApprovedFile(dir, raw string) (string, bool) {
	list, _ := currentHostProjectDirStore()
	if list == nil {
		return "", false
	}
	root, ok := hostdir.MatchApproved(dir, list())
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		return "", false
	}
	cleaned := filepath.Clean(raw)
	if !isInside(root, cleaned) {
		return "", false
	}
	// 软链接跟随后再校验一次，否则项目内一个指向 /etc 的链接就能读外部文件。
	real, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", false
	}
	// 根目录本身也要先解析：macOS 上 /var 是 /private/var 的符号链接，
	// 用户经选择框授权的是 /var/...，解析后的真实路径却是 /private/var/...，
	// 拿未解析的根去比较会把项目内所有文件都误判成越界。
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	if !isInside(realRoot, real) {
		return "", false
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return real, true
}

func isInside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
// DownloadHostProjectFile 以流式响应返回一个已授权项目目录内的文件。
//
// 走 GET + 两个 query 参数（dir/path）而不是 POST，浏览器直接点链接即可下载，
// 前端不必先把整份文件读进内存再拼 Blob。
//
// 只读：不设 Content-Disposition 之外的任何副作用，也不缓存到服务端。
func (*SystemHandler) DownloadHostProjectFile(c *gin.Context) {
	real, ok := resolveApprovedFile(c.Query("dir"), c.Query("path"))
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error":   gin.H{"message": "文件不在已授权的项目目录内，或不可读"},
		})
		return
	}
	info, err := os.Stat(real)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"message": "文件不存在"},
		})
		return
	}
	if info.Size() > projectTreeMaxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"error":   gin.H{"message": "文件超过 200MB，请直接在 Finder 中打开"},
		})
		return
	}
	// ServeFile 负责 Range、If-Modified-Since 与正确的 Content-Type，
	// 自己 WriteHeader 反而会丢掉这些语义。
	c.Header("Content-Disposition",
		"attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(real)))
	c.File(real)
}
