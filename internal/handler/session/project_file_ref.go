package session

// 项目文件引用：把项目目录里的文件（或目录清单）登记成当前会话的临时附件。
//
// 为什么不走上传：文件已经在服务端所在机器的磁盘上，让浏览器先读出来再
// multipart 传回去，等于把同一份字节搬两遍，几十兆的 pdf 会很慢。这里直接
// 打开文件喂给 temporaryDocumentService.Create，前端拿到的 attachment id 与
// 普通上传完全一样，后续解析 / 注入上下文 / 预览都不用改。
//
// 只读约束：本文件不写、不删、不改项目目录里的任何东西；唯一的写入是把
// 内容复制一份进应用自己的附件存储（temporary_documents），那里的生命周期
// 由 CleanupExpired 管理。

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
)

// 授权校验由根 handler 包注入：它持有已授权项目目录列表，而本包被根包
// import，直接反向 import 会形成循环依赖。
var approvedProjectPathMatcher func(dir string) (string, bool)

// SetApprovedProjectPathMatcher installs the project-directory boundary check.
// Nil on deployments without the desktop project picker: the route then 404s.
func SetApprovedProjectPathMatcher(fn func(dir string) (string, bool)) {
	approvedProjectPathMatcher = fn
}

func matchApprovedProjectPath(dir string) (string, bool) {
	if approvedProjectPathMatcher == nil {
		return "", false
	}
	return approvedProjectPathMatcher(dir)
}

const (
	// 目录清单不读内容，但仍然限制条数，避免把几万个路径塞进上下文。
	projectRefMaxListEntries = 400
	// 清单文本的字符预算：按模型上下文估算，约 1.2 万字符。
	projectRefMaxManifestChars = 12000
)

// projectFileRefRequest 是 POST /sessions/:session_id/project-refs 的请求体。
type projectFileRefRequest struct {
	// Dir 是项目根目录（必须是已授权目录）。后端用它做越界校验，
	// 不接受前端传进来的任意路径。
	Dir string `json:"dir" binding:"required"`
	// Path 是相对 Dir 的路径；为空表示引用整个项目根目录。
	Path string `json:"path"`
	// Kind 为 "file" 或 "dir"，缺省按文件系统实际类型判断。
	Kind string `json:"kind"`
	// AgentID 让后端沿用智能体的解析器选择规则。
	AgentID string `json:"agent_id"`
	// AgentSourceTenantID 用于共享智能体场景。
	AgentSourceTenantID string `json:"agent_source_tenant_id"`
}

// projectFileRefResponse 返回登记结果，前端只需 attachment_id。
type projectFileRefResponse struct {
	AttachmentID string `json:"attachment_id"`
	FileName     string `json:"file_name"`
	FileSize     int64  `json:"file_size"`
	Kind         string `json:"kind"`
}

// CreateProjectFileRef 把项目目录里的一个文件或一个目录清单登记为会话附件。
func (h *Handler) CreateProjectFileRef(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("session_id")
	// 与会话附件上传保持一致：只有会话所有者能把内容挂进自己的对话。
	session, err := h.sessionService.GetOwnedSession(ctx, sessionID)
	if err != nil || session == nil {
		c.Error(apperrors.NewNotFoundError("Session not found"))
		return
	}

	var req projectFileRefRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(fmt.Sprintf("invalid project reference: %v", err)))
		return
	}

	root, ok := matchApprovedProjectPath(req.Dir)
	if !ok {
		logger.Warnf(ctx, "[ProjectRef] rejected unapproved dir=%q session=%s", req.Dir, sessionID)
		c.Error(apperrors.NewForbiddenError("目录未获得访问授权，请通过系统目录选择框选择"))
		return
	}

	target, kind, err := resolveProjectRefTarget(root, req.Path)
	if err != nil {
		logger.Warnf(ctx, "[ProjectRef] resolve failed dir=%q path=%q kind=%q err=%v",
			req.Dir, req.Path, req.Kind, err)
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	if req.Kind == "file" || req.Kind == "dir" {
		kind = req.Kind
	}

	tenantID := session.TenantID
	if tenantID == 0 {
		tenantID = c.GetUint64(types.TenantIDContextKey.String())
	}
	// 临时文档服务按 execution tenant 查解析器与存储配置。
	opCtx := types.WithExecutionTenant(ctx, tenantID)

	sourceTenantID, _ := types.ParseAgentSourceTenantID(req.AgentSourceTenantID)
	agent, resourceTenantID, _ := h.resolveAgent(ctx, c, req.AgentID, sourceTenantID)

	var document *types.TemporaryDocument
	if kind == "dir" {
		document, err = h.createProjectDirManifest(opCtx, tenantID, sessionID, root, target, agent, resourceTenantID)
	} else {
		document, err = h.createProjectFileAttachment(opCtx, tenantID, sessionID, target, agent, resourceTenantID)
	}
	if err != nil {
		logger.Warnf(ctx, "[ProjectRef] create failed session=%s kind=%s err=%v", sessionID, kind, err)
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "[ProjectRef] attached session=%s kind=%s name=%s size=%d",
		sessionID, kind, document.FileName, document.FileSize)
	c.JSON(http.StatusAccepted, gin.H{
		"success": true,
		"data": projectFileRefResponse{
			AttachmentID: document.ID,
			FileName:     document.FileName,
			FileSize:     document.FileSize,
			Kind:         kind,
		},
	})
}

// resolveProjectRefTarget 把路径规范化成绝对路径，并保证它没有跳出 root。
//
// rel 既接受相对 root 的路径，也接受绝对路径——文件浏览面板返回的是绝对
// 路径，而外部调用方可能传相对路径，两种都收敛到同一个 candidate。
func resolveProjectRefTarget(root, rel string) (string, string, error) {
	raw := strings.TrimSpace(rel)
	var candidate string
	if raw == "" {
		candidate = root
	} else if filepath.IsAbs(raw) {
		// 绝对路径：不再拼接，但仍由下面的 pathInside 决定是否放行。
		candidate = filepath.Clean(raw)
	} else {
		candidate = filepath.Join(root, filepath.FromSlash(path.Clean(raw)))
	}
	if !pathInside(root, candidate) {
		return "", "", fmt.Errorf("路径不在项目目录内")
	}
	// 软链接可以指到目录外面，必须解析后再校验一次。
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("文件不存在（可能已被移动或删除），请刷新文件列表后重试")
		}
		// 权限问题在 macOS 上表现为 EPERM（TCC 拦截），与"不存在"必须分开说：
		// 前者要用户去系统设置里授权，后者只需要刷新。
		return "", "", fmt.Errorf("无权访问该文件（%v），请在系统设置的隐私与安全性中允许本应用访问该位置", err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("项目目录不可访问（%v）", err)
	}
	if !pathInside(realRoot, real) {
		return "", "", fmt.Errorf("路径不在项目目录内")
	}
	info, err := os.Stat(real)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("文件不存在（可能已被移动或删除），请刷新文件列表后重试")
		}
		return "", "", fmt.Errorf("无权访问该文件（%v），请在系统设置的隐私与安全性中允许本应用访问该位置", err)
	}
	if info.IsDir() {
		return real, "dir", nil
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("不支持引用该类型的文件")
	}
	return real, "file", nil
}

// pathInside 判断 p 是否位于 root 之内（root 自身算在内）。
func pathInside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// createProjectFileAttachment 打开磁盘文件并登记为临时附件。
//
// Create 内部会把 reader 全量读进内存并复制进附件存储，所以这里只需要一个
// 受大小限制的 reader：上限由 secutils.GetMaxFileSize 在服务内部强制执行。
func (h *Handler) createProjectFileAttachment(
	ctx context.Context,
	tenantID uint64,
	sessionID, absPath string,
	agent *types.CustomAgent,
	resourceTenantID uint64,
) (*types.TemporaryDocument, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("文件不可访问")
	}
	if info.IsDir() {
		return nil, fmt.Errorf("目录不能按文件引用")
	}
	if info.Size() <= 0 {
		return nil, fmt.Errorf("文件为空，没有可引用的内容")
	}

	file, err := os.Open(absPath)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败：%v", err)
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(absPath))
	options := types.TemporaryDocumentCreateOptions{ResourceTenantID: resourceTenantID}
	if agent != nil {
		options.ParserEngine = agent.Config.ResolveChatParserEngine(ext)
	}

	return h.temporaryDocuments.Create(
		ctx, tenantID, sessionID,
		filepath.Base(absPath), mimeTypeOf(absPath), info.Size(), file, options,
	)
}

// createProjectDirManifest 把一个目录下的文件清单做成一个 md 附件。
//
// 目录本身没有"内容"可解析，直接把清单当上下文：模型能知道目录里有什么、
// 各自多大、路径是什么，需要细看时再让用户单独 @ 那个文件。
func (h *Handler) createProjectDirManifest(
	ctx context.Context,
	tenantID uint64,
	sessionID, root, dirPath string,
	agent *types.CustomAgent,
	resourceTenantID uint64,
) (*types.TemporaryDocument, error) {
	manifest, err := buildProjectDirManifest(root, dirPath)
	if err != nil {
		return nil, err
	}
	relName := strings.TrimPrefix(strings.TrimPrefix(dirPath, root), string(filepath.Separator))
	if relName == "" {
		relName = filepath.Base(root)
	}
	fileName := strings.ReplaceAll(relName, string(filepath.Separator), "_") + "_目录清单.md"

	options := types.TemporaryDocumentCreateOptions{ResourceTenantID: resourceTenantID}
	return h.temporaryDocuments.Create(
		ctx, tenantID, sessionID,
		fileName, "text/markdown", int64(len(manifest)),
		strings.NewReader(manifest), options,
	)
}

// buildProjectDirManifest 生成目录清单的 markdown 文本。
func buildProjectDirManifest(root, dirPath string) (string, error) {
	type entry struct {
		rel  string
		dir  bool
		size int64
	}
	var entries []entry
	truncated := false

	walkErr := filepath.WalkDir(dirPath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// 单个条目读不到不该让整次引用失败，跳过即可。
			if p == dirPath {
				return err
			}
			return nil
		}
		if p == dirPath {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if _, skip := projectRefSkipDirs[name]; skip {
				return filepath.SkipDir
			}
		} else if strings.HasPrefix(name, ".") {
			return nil
		}
		if len(entries) >= projectRefMaxListEntries {
			truncated = true
			return filepath.SkipAll
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(dirPath, p)
		entries = append(entries, entry{rel: filepath.ToSlash(rel), dir: d.IsDir(), size: info.Size()})
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("读取目录失败：%v", walkErr)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].dir != entries[j].dir {
			return entries[i].dir
		}
		return entries[i].rel < entries[j].rel
	})

	relDir, _ := filepath.Rel(root, dirPath)
	title := filepath.ToSlash(relDir)
	if title == "." {
		title = filepath.Base(root)
	} else {
		title = "/" + title
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# 目录清单：%s\n\n", title)
	fmt.Fprintf(&b, "共 %d 项", len(entries))
	if truncated {
		fmt.Fprintf(&b, "（已达 %d 项上限，仅列出前 %d 项）", projectRefMaxListEntries, projectRefMaxListEntries)
	}
	b.WriteString("。目录用结尾的 `/` 标识。\n\n")

	written := 0
	for _, e := range entries {
		line := "- `" + e.rel
		if e.dir {
			line += "/`"
		} else {
			line += "` （" + humanSize(e.size) + "）"
		}
		if b.Len()+len(line) > projectRefMaxManifestChars {
			fmt.Fprintf(&b, "\n……其余 %d 项因长度限制未列出。\n", len(entries)-written)
			break
		}
		b.WriteString(line + "\n")
		written++
	}
	return b.String(), nil
}

// projectRefSkipDirs 与目录浏览面板保持一致的噪音目录。
var projectRefSkipDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, ".venv": {}, "venv": {}, "__pycache__": {},
	".next": {}, "dist": {}, "build": {}, ".cache": {}, ".idea": {},
}

func mimeTypeOf(p string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(p))); t != "" {
		return t
	}
	return "application/octet-stream"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}