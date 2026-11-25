//go:build licadmin_native

// 原生窗口版（构建标签 licadmin_native）。
//
// 为什么要它：浏览器模式能用，但「双击一个 app，结果跳进浏览器」在交付现场
// 观感很差——地址栏、书签栏、另一个窗口，用户会怀疑这是个网页而不是软件。
// 这里用系统 WKWebView 起一个独立窗口，关掉窗口就等于退出。
//
// 为什么用 webview_go 而不是跟着客户端用 Wails：Wails 的窗口通过自定义 scheme
// （wails://wails/）加载页面，而自定义 scheme 上 Cookie 不生效——管理端的身份
// 恰恰是 HttpOnly 会话 Cookie。要么改认证协议（把会话搬到请求头/localStorage，
// 动到已经审计过的鉴权面），要么换一个能把窗口直接指向 http://127.0.0.1 的壳。
// 后者代价小得多：会话、上传、下载都走真实 http 源，认证代码一行不用改。
//
// 代价也要说清楚：webview_go 只提供窗口与 JS 绑定，没有菜单栏、没有下载代理。
// 所以「备份下载」在这个壳里改成由 Go 直接落盘到 ~/Downloads（见 bindSaveBackup），
// 而不是依赖浏览器的下载行为——WKWebView 不给下载代理就什么都不会发生。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	webview "github.com/webview/webview_go"

	"github.com/Tencent/WeKnora/internal/license/licadmin"
)

func init() {
	nativeShellStart = runNativeShell
}

// 窗口尺寸：默认按 1320×880 开，最小 1040×720。
//
// 管理端的表格（授权台账、发布包）列多，窄于 1040 就要横向滚动，
// 而那正是「看起来不像专业后台」的观感来源。
const (
	nativeWindowWidth  = 1320
	nativeWindowHeight = 880
	nativeWindowMinW   = 1040
	nativeWindowMinH   = 720
)

// runNativeShell 打开窗口并阻塞到窗口关闭。
func runNativeShell(opts nativeShellOptions) {
	w := webview.New(false)
	defer w.Destroy()

	w.SetTitle("国研智研体授权管理")
	w.SetSize(nativeWindowWidth, nativeWindowHeight, webview.HintNone)
	w.SetSize(nativeWindowMinW, nativeWindowMinH, webview.HintMin)

	// 页面侧据此把「浏览器下载」换成「Go 落盘保存」，并隐藏只对浏览器有意义的提示。
	w.Init("window.__licadminNative = true;")

	// 系统快捷键（⌘Q/⌘W/⌘M，以及输入框的 ⌘C/⌘V/⌘X/⌘A）由 NSApplication 按主菜单
	// 的 keyEquivalent 派发，webview_go 不建菜单，所以这里自己装一份。
	// 必须在 Run() 之前装：菜单是窗口起来后用户第一眼就能按的东西。
	if n := installNativeMenu(); n > 0 {
		opts.Logf("已安装系统菜单 %d 项（⌘Q 退出 / ⌘W 关闭 / ⌘M 最小化 / 编辑快捷键），快捷键回查：%s",
			n, describeShortcutMask(menuShortcutMask()))
	} else {
		opts.Logf("系统菜单未安装（非 macOS 平台或 AppKit 未就绪），快捷键由窗口自行处理")
	}

	if err := w.Bind("licadminSaveBackup", func(includeReleases bool) (string, error) {
		return bindSaveBackup(opts, includeReleases)
	}); err != nil {
		opts.Logf("绑定备份保存失败：%v", err)
	}

	if err := w.Bind("licadminAppInfo", func() (map[string]string, error) {
		return map[string]string{
			"version": opts.Version,
			"dataDir": opts.DataDir,
			"mode":    "native",
		}, nil
	}); err != nil {
		opts.Logf("绑定应用信息失败：%v", err)
	}

	w.Navigate(opts.URL + "/")
	w.Run()
}

// describeShortcutMask 把回查结果翻成人能看的清单。
func describeShortcutMask(mask int) string {
	if mask == 0 {
		return "无（主菜单没被 AppKit 接住）"
	}
	names := []struct {
		bit  int
		name string
	}{
		{1, "⌘Q"}, {2, "⌘W"}, {4, "⌘M"}, {8, "⌘C"}, {16, "⌘V"},
	}
	out := ""
	for _, it := range names {
		if mask&it.bit == 0 {
			continue
		}
		if out != "" {
			out += " "
		}
		out += it.name
	}
	return out
}

// bindSaveBackup 把备份直接写到 ~/Downloads，返回落盘路径。
//
// 为什么要绕开前端那条 fetch→blob→<a download> 的路：WKWebView 没有下载代理时，
// 点 <a download> 不会有任何反应，也不报错——用户会以为「备份按钮坏了」。
// 这里直接复用服务端同一个 BuildBackup（VACUUM INTO 导出 db 再打包），
// 写盘位置选 ~/Downloads，用户点完就能在访达里找到。
func bindSaveBackup(opts nativeShellOptions, includeReleases bool) (string, error) {
	if opts.Built == nil || opts.Built.auth == nil {
		return "", fmt.Errorf("管理端尚未就绪")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("找不到用户目录：%w", err)
	}
	dir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("无法创建下载目录：%w", err)
	}
	name := fmt.Sprintf("licadmin-backup-%s.zip", time.Now().Format("20060102-150405"))
	out := filepath.Join(dir, name)
	info, err := licadmin.BuildBackup(opts.Built.auth.DB(), opts.DataDir, out, includeReleases, time.Now())
	if err != nil {
		return "", fmt.Errorf("生成备份失败：%w", err)
	}
	opts.Logf("备份已保存到 %s（%d 字节，%d 个条目）", out, info.SizeBytes, len(info.Entries))
	return out, nil
}
