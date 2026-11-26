//go:build windows

package main

import "golang.org/x/sys/windows"

// notify 弹一个模态消息框。
//
// failure 为 true 时用错误图标，并且置顶 + 抢占前台：启动器的全部价值就在于
// 「用户看得见」。之前主程序被拦时是静默消失，这一点必须扭转过来。
func notify(message string, failure bool) error {
	title, err := windows.UTF16PtrFromString(appName + " · 启动器")
	if err != nil {
		return err
	}
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	flags := uint32(windows.MB_OK | windows.MB_ICONINFORMATION)
	if failure {
		flags = windows.MB_OK | windows.MB_ICONERROR | windows.MB_SETFOREGROUND | windows.MB_TOPMOST
	}
	_, err = windows.MessageBox(0, text, title, flags)
	return err
}
