package licadmin

import (
	"embed"
	"io/fs"
	"net/http"
)

// 前端资源用 go:embed 打进二进制，而不是放在磁盘目录里。
//
// 原因：管理端最终以 .app 双击启动，二进制从任意工作目录被调用；如果页
// 面从磁盘读，用户换一台机器（或从 Finder 启动）就会看到 404。内嵌之后
// 「一个二进制 = 完整的发码端」，备份目录只要管 licadmin-data。
//
//go:embed assets
var embeddedAssets embed.FS

// assetFS 是去掉 assets/ 前缀后的文件树，路径形如 index.html、app.css。
var assetFS = func() fs.FS {
	sub, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		// 只会在 embed 声明写错时发生，属于编译期问题，直接 panic 比
		// 静默返回空页面更容易被发现。
		panic("licadmin: 内嵌资源缺失：" + err.Error())
	}
	return sub
}()

// handleIndex 返回管理页面。
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(assetFS, "index.html")
	if err != nil {
		http.Error(w, "管理页面缺失："+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	// 禁用缓存：避免改了页面后浏览器还拿旧的。
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// handleAssets 提供 index.html 引用的 CSS/JS。
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request) {
	// 同样禁用缓存：页面与脚本的版本始终随二进制一起变，缓存反而是负担。
	w.Header().Set("Cache-Control", "no-store")
	http.StripPrefix("/assets/", http.FileServer(http.FS(assetFS))).ServeHTTP(w, r)
}
