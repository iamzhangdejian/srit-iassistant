package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// RevealApprovedProjectDir 在系统文件管理器中打开（并选中）一个目录。
//
// 为什么必须走原生进程而不是前端 window.open：
//   - cmd/desktop/main.go 注入的脚本把 window.open 改写成只放行 http/https，
//     file:// 会落到浏览器原生的 window.open；
//   - WKWebView 默认不允许 webview 导航到 file:// 路径，且没有实现
//     UIDelegate 的相关回调，于是点击「在文件管理器中打开」静默失败。
//
// 安全边界不在这里：调用方（handler.RevealHostProjectDir）已经校验过该目录
// 在用户授权的白名单里，这里只负责把「已经合法的目录」交给系统。
func RevealApprovedProjectDir(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("目录为空")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("无法解析目录路径: %w", err)
	}

	switch runtime.GOOS {
	case "darwin":
		// -R 是「在 Finder 中显示」，比 `open <dir>` 更精确：
		// 它打开的窗口里该目录处于选中态，用户一眼能定位。
		return exec.Command("open", "-R", abs).Start()
	case "windows":
		return exec.Command("explorer", "/select,"+abs).Start()
	default:
		// Linux 桌面环境下 xdg-open 是约定的入口；没有桌面时它会失败，
		// 错误原样返回给前端展示。
		return exec.Command("xdg-open", abs).Start()
	}
}

// revealPathInFileManager 把任意已授权项目内的路径交给系统文件管理器
// （文件会被选中，目录会被打开）。
//
// 与 RevealApprovedProjectDir 分开是因为那个函数是给 HTTP handler 用的，
// 它信任调用方已经校验过白名单；这里的调用方是 webview 里的 JS，所以
// 必须在进程内再校验一次：只有落在用户授权过的项目目录里的路径才允许
// 交给系统。这样即便前端被 XSS 注入，也只能打开用户自己授权过的目录。
func revealPathInFileManager(path string) error {
	abs, err := resolveApprovedPath(path)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", abs).Start()
	case "windows":
		return exec.Command("explorer", "/select,"+abs).Start()
	default:
		return exec.Command("xdg-open", abs).Start()
	}
}

// openPathInSystemApp 用系统的默认应用打开一个文件。
//
// 这是预览面板的兜底出口：PDF 可以内置渲染，.doc/.ppt 走后端转换，但
// 转换失败的复杂文档（公式、嵌入对象、扫描件）只有原应用能正确显示，
// 与其在应用里画出一个残缺的页面，不如直接交给系统。
func openPathInSystemApp(path string) error {
	abs, err := resolveApprovedPath(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("文件不存在或不可访问: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("该路径是目录，请在文件管理器中打开")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", abs).Start()
	case "windows":
		// cmd /c start 需要空标题参数，否则带引号的路径会被当成窗口标题。
		return exec.Command("cmd", "/c", "start", "", abs).Start()
	default:
		return exec.Command("xdg-open", abs).Start()
	}
}

// resolveApprovedPath 把前端给的路径规范化，并要求它位于已授权的项目目录内。
//
// 用 filepath.EvalSymlinks 解析后再比对：项目目录本身可能是软链接（用户
// 经常把 ~/work 指到外置盘），而项目里的软链接不能成为逃出白名单的通道。
func resolveApprovedPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("路径为空")
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("必须是绝对路径: %s", trimmed)
	}
	resolved, err := filepath.EvalSymlinks(trimmed)
	if err != nil {
		return "", fmt.Errorf("路径不存在或不可访问: %w", err)
	}
	for _, dir := range LoadProjectDirs() {
		root, err := filepath.EvalSymlinks(strings.TrimSpace(dir))
		if err != nil {
			continue
		}
		if resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("路径不在已授权的项目目录内: %s", trimmed)
}
