//go:build !bindings

package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Tencent/WeKnora/internal/logger"
)

// resolveDesktopWebDir 找到要交给 Gin 的前端静态目录。
//
// 背景：Wails 窗口里的请求会经过 AssetServer.Handler 反向代理到本进程的 Gin，
// 而 Gin 只有在设置了 WEKNORA_WEB_DIR 时才注册前端静态路由
// （internal/router/static.go:19-27）。没设时候 "/" 会落到全局鉴权中间件上返回
// 401，进而让 Wails 无法把 /wails/runtime.js 注入 HTML，window.go 也就不存在，
// 前端的 autoSetup() 拿不到 setup token，用户只能看到
// {"error":"Unauthorized: missing authentication"}。
//
// 查找顺序：
//  1. <Resources>/web —— 打包时由 scripts/package-*.sh 把 frontend/dist 放进来
//  2. <可执行文件目录>/web —— 非 .app 的分发方式（tar 包、Windows 安装目录）
//  3. 仓库里的 frontend/dist —— wails dev / 本地直跑二进制时用
//
// 三个位置都要求存在 index.html，避免把空目录当资源根。
func resolveDesktopWebDir(hasConfigRoot bool) (string, bool) {
	var candidates []string
	if hasConfigRoot {
		cwd, err := os.Getwd()
		if err == nil {
			candidates = append(candidates, filepath.Join(cwd, "web"))
		}
	}
	if execPath, err := os.Executable(); err == nil {
		base := filepath.Dir(execPath)
		candidates = append(candidates,
			filepath.Join(base, "web"),
			// .app 分发：Contents/MacOS/<bin> → Contents/Resources/web
			filepath.Join(base, "..", "Resources", "web"),
		)
	}
	// 仓库布局：<repo>/cmd/desktop/build/bin/<bin> → <repo>/frontend/dist
	candidates = append(candidates, filepath.Join("..", "..", "..", "..", "frontend", "dist"))

	for _, dir := range candidates {
		if abs, err := filepath.Abs(dir); err == nil {
			if _, statErr := os.Stat(filepath.Join(abs, "index.html")); statErr == nil {
				logger.Infof(context.Background(), "[Desktop] Serving web assets from %s", abs)
				return abs, true
			}
		}
	}
	logger.Warnf(context.Background(),
		"[Desktop] No web assets found; the window will show a 401 page. "+
			"Run the packaging script or set WEKNORA_WEB_DIR to a built frontend.")
	return "", false
}
