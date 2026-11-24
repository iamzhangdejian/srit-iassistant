package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// withApprovedDir 把 dir 设为唯一授权目录，测试结束自动恢复。
func withApprovedDir(t *testing.T, dir string) {
	t.Helper()
	SetHostProjectDirStore(func() []string { return []string{dir} }, nil)
	t.Cleanup(func() { SetHostProjectDirStore(nil, nil) })
}

func getProjectFiles(t *testing.T, dir string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/system/project-files?dir="+urlQueryEscape(dir),
		nil,
	)
	(&SystemHandler{}).ListHostProjectFiles(c)
	return w
}

func TestListHostProjectFilesRejectsUnapprovedDir(t *testing.T) {
	root := t.TempDir()
	withApprovedDir(t, root)

	other := t.TempDir()
	w := getProjectFiles(t, other)
	require.Equal(t, http.StatusForbidden, w.Code,
		"目录不在授权列表里必须 403，否则面板能用来浏览任意目录")
}

func TestListHostProjectFilesUnavailableWithoutStore(t *testing.T) {
	SetHostProjectDirStore(nil, nil)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/project-files?dir=/tmp", nil)
	(&SystemHandler{}).ListHostProjectFiles(c)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestListHostProjectFilesListsFilesAndSkipsNoise(t *testing.T) {
	root := t.TempDir()
	withApprovedDir(t, root)

	require.NoError(t, os.WriteFile(filepath.Join(root, "report.md"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "node_modules", "pkg", "index.js"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs", "spec.md"), []byte("y"), 0o644))

	w := getProjectFiles(t, root)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data projectTreeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, root, body.Data.Root)

	names := make([]string, 0, len(body.Data.Entries))
	for _, entry := range body.Data.Entries {
		names = append(names, entry.Rel)
	}
	require.Contains(t, names, "report.md")
	require.Contains(t, names, "docs/spec.md", "子目录里的文件要列出来")
	require.NotContains(t, names, ".hidden", "隐藏文件跳过")
	require.NotContains(t, names, "node_modules", "构建目录跳过")
	require.NotContains(t, names, "node_modules/pkg/index.js")

	for _, entry := range body.Data.Entries {
		if entry.Rel == "report.md" {
			require.Equal(t, "file", entry.Kind)
			require.True(t, entry.Downloadable)
			require.Equal(t, int64(5), entry.Size)
		}
		if entry.Rel == "docs" {
			require.Equal(t, "dir", entry.Kind, "目录行用于展示层级")
			require.False(t, entry.Downloadable, "目录不可下载")
		}
	}
}

func TestResolveApprovedFileBlocksEscapes(t *testing.T) {
	root := t.TempDir()
	withApprovedDir(t, root)

	inside := filepath.Join(root, "ok.txt")
	require.NoError(t, os.WriteFile(inside, []byte("ok"), 0o644))

	resolved, ok := resolveApprovedFile(root, inside)
	require.True(t, ok)
	// 返回值是解析软链接后的真实路径（macOS 上 /var → /private/var），
	// 只要指向同一份文件即可，不要求字面相等。
	resolvedReal, err := filepath.EvalSymlinks(inside)
	require.NoError(t, err)
	require.Equal(t, resolvedReal, resolved)

	// ../ 越界
	_, ok = resolveApprovedFile(root, filepath.Join(root, "..", "escape.txt"))
	require.False(t, ok, "相对路径越界必须被拒")

	// 项目内的软链接指向外部：跟随之后再校验，必须拒
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0o644))
	link := filepath.Join(root, "link.txt")
	require.NoError(t, os.Symlink(outsideFile, link))
	_, ok = resolveApprovedFile(root, link)
	require.False(t, ok, "指向项目外的软链接必须被拒，否则一个链接就能读到任意文件")

	// 目录不是文件
	_, ok = resolveApprovedFile(root, root)
	require.False(t, ok, "目录不可下载")
}