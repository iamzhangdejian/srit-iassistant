//go:build windows

package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// notifyBootFailure 在 Windows 上用系统弹窗把启动失败说清楚。
//
// 没有这一步，启动期 panic 的结果就是「双击没反应」：进程没有控制台，也没有
// 任何窗口，用户完全无从下手。弹窗里给出日志路径，用户可以直接把文件发回来。
func notifyBootFailure(execPath string, recovered any) {
	logPath := bootLogPath(execPath)
	hint := ""
	if strings.Contains(strings.ToLower(fmt.Sprint(recovered)), "webview2") {
		hint = "\n\n缺少 Microsoft Edge WebView2 运行时。请安装后重试：\n" +
			"https://go.microsoft.com/fwlink/p/?LinkId=2124703"
	}
	text := fmt.Sprintf("国研智研体启动失败：\n%v%s\n\n诊断日志：\n%s", recovered, hint, logPath)
	caption := "国研智研体"

	textPtr, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	captionPtr, err := windows.UTF16PtrFromString(caption)
	if err != nil {
		return
	}
	const mbIconError = 0x00000010
	const mbOK = 0x00000000
	_, _ = windows.MessageBox(0, textPtr, captionPtr, mbIconError|mbOK)
}
