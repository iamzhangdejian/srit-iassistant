//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// notifyBootFailure 在 Windows 上用系统弹窗把启动失败说清楚。
//
// 没有这一步，启动期 panic 的结果就是「双击没反应」：进程没有控制台，也没有
// 任何窗口，用户完全无从下手。弹窗里给出日志路径，用户可以直接把文件发回来。
func notifyBootFailure(execPath string, recovered any) {
	logPath := bootLogPath(execPath)
	text := fmt.Sprintf("国研智研体启动失败：\n%v\n\n诊断日志：\n%s", recovered, logPath)
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
