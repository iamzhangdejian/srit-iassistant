package handler

import (
	"net/http"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/hostdir"
)

// hostProjectPicker opens the native folder dialog on Lite desktop.
// Nil on every other deployment: the route then 404s.
var (
	hostProjectPickerMu sync.RWMutex
	hostProjectPicker   func() (string, error)
	listProjectDirs     func() []string
	removeProjectDir    func(string) error
	revealProjectDir    func(string) error
)

// SetHostProjectPicker is called by cmd/desktop. Standard servers leave it nil.
func SetHostProjectPicker(fn func() (string, error)) {
	hostProjectPickerMu.Lock()
	hostProjectPicker = fn
	hostProjectPickerMu.Unlock()
}

// SetHostProjectDirStore installs the read/revoke hooks for the approved
// project-directory list. Like the picker, only cmd/desktop supplies these;
// without them the listing/revoke routes 404, so a plain server never
// pretends to manage host directories it does not have.
//
// The two hooks together form the *revocation* path. Without them a user could
// pick a folder (granting the sandbox read/write) but never take that grant
// back, because the SPA reaches this process over HTTP and cannot call the
// Wails bindings that expose RemoveProjectDir.
func SetHostProjectDirStore(list func() []string, remove func(string) error) {
	hostProjectPickerMu.Lock()
	listProjectDirs = list
	removeProjectDir = remove
	hostProjectPickerMu.Unlock()
}

// HostProjectDirSupported reports whether this process can hand out project
// directories at all: the native picker is what turns a folder into an approved
// one, so without it a project can never be created and the composer has no way
// to file a session under a directory.
//
// It is deliberately independent of the host *sandbox*: Windows ships without a
// sandbox backend (internal/localsandbox/winhost reports unsupported), but
// projects, their files and the project tree still work there.
func HostProjectDirSupported() bool {
	return currentHostProjectPicker() != nil
}

func currentHostProjectPicker() func() (string, error) {
	hostProjectPickerMu.RLock()
	defer hostProjectPickerMu.RUnlock()
	return hostProjectPicker
}

func currentHostProjectDirStore() (func() []string, func(string) error) {
	hostProjectPickerMu.RLock()
	defer hostProjectPickerMu.RUnlock()
	return listProjectDirs, removeProjectDir
}

// MatchApprovedProjectPath checks that dir is inside one of the approved
// project directories and returns the clean absolute root it resolved to.
//
// The session package needs this to turn an @-referenced file into an
// attachment: the boundary must be enforced by the package that owns the
// approval list, not re-implemented by the caller.
func MatchApprovedProjectPath(dir string) (string, bool) {
	list, _ := currentHostProjectDirStore()
	if list == nil {
		return "", false
	}
	matched, ok := hostdir.MatchApproved(dir, list())
	if !ok {
		return "", false
	}
	abs, err := filepath.Abs(matched)
	if err != nil {
		return "", false
	}
	return abs, true
}

// SetHostProjectDirRevealer installs the hook that opens a directory in the
// system file manager (Finder/Explorer). Like the picker, only cmd/desktop
// supplies it; without it the reveal route 404s, so a plain server never
// claims to open windows on a machine it does not control.
//
// The hook is a *display* action, not an authorization path: the handler
// still refuses any directory that is not on the approved list, so a caller
// cannot use it to browse the filesystem.
func SetHostProjectDirRevealer(fn func(string) error) {
	hostProjectPickerMu.Lock()
	revealProjectDir = fn
	hostProjectPickerMu.Unlock()
}

func currentHostProjectDirRevealer() func(string) error {
	hostProjectPickerMu.RLock()
	defer hostProjectPickerMu.RUnlock()
	return revealProjectDir
}

// PickHostProjectDir opens the system folder picker. The SPA talks HTTP to
// this process; Wails JS bindings are not injected through the
// reverse-proxied frontend, so this is the working authorization path.
func (*SystemHandler) PickHostProjectDir(c *gin.Context) {
	picker := currentHostProjectPicker()
	if picker == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"message": "host project picker is not available"},
		})
		return
	}
	dir, err := picker()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"message": err.Error()},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{"dir": dir},
	})
}

// ListHostProjectDirs returns the directories the user has approved so far,
// so the UI can show what the sandbox may currently reach.
func (*SystemHandler) ListHostProjectDirs(c *gin.Context) {
	list, _ := currentHostProjectDirStore()
	if list == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"message": "host project directories are not available"},
		})
		return
	}
	dirs := list()
	if dirs == nil {
		// 返回空数组而不是 null，前端不必额外判空
		dirs = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{"dirs": dirs},
	})
}

type revokeHostProjectDirRequest struct {
	Dir string `json:"dir" binding:"required"`
}

// RevokeHostProjectDir removes one directory from the approved list, revoking
// the sandbox's access to it. Revocation is deliberately allowed while a
// session still points at the directory: the session keeps its recorded path,
// but the sandbox refuses to touch it once the approval is gone, which is the
// safe failure direction.
func (*SystemHandler) RevokeHostProjectDir(c *gin.Context) {
	_, remove := currentHostProjectDirStore()
	if remove == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"message": "host project directories are not available"},
		})
		return
	}

	var req revokeHostProjectDirRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"message": err.Error()},
		})
		return
	}

	if err := remove(req.Dir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{"dir": req.Dir, "revoked": true},
	})
}

type revealHostProjectDirRequest struct {
	// Dir 必须是用户通过系统目录选择框授权过的目录之一。
	Dir string `json:"dir" binding:"required"`
}

// RevealHostProjectDir 在系统文件管理器中打开一个已授权目录。
//
// 为什么需要这个接口：SPA 是反向代理形态，前端拿不到 Wails 的 JS 绑定，
// 而 window.open("file://…") 在 WKWebView 里会被拒绝（Wails 注入的
// window.open 改写只放行 http/https），点击后毫无反应。
//
// 安全边界：只接受授权白名单里的目录，未授权的路径一律 403。这一点很
// 关键——否则这个接口就变成了「任意路径浏览器」，可以拿它探测文件系统。
func (*SystemHandler) RevealHostProjectDir(c *gin.Context) {
	reveal := currentHostProjectDirRevealer()
	if reveal == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   gin.H{"message": "host project directories are not available"},
		})
		return
	}

	var req revealHostProjectDirRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"message": err.Error()},
		})
		return
	}

	list, _ := currentHostProjectDirStore()
	var approved []string
	if list != nil {
		approved = list()
	}
	dir, ok := hostdir.MatchApproved(req.Dir, approved)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"error":   gin.H{"message": "目录未获得访问授权，请通过系统目录选择框选择"},
		})
		return
	}

	if err := reveal(dir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"message": err.Error()},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": gin.H{"dir": dir, "revealed": true},
	})
}
