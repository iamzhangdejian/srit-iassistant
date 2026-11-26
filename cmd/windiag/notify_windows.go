//go:build windows

package main

import "golang.org/x/sys/windows"

// notify 弹一个模态消息框。windiag 的价值就在于「用户看得见」，
// 所以这里必须真的弹窗，而不是悄悄写日志。
func notify(message string) error {
	title, err := windows.UTF16PtrFromString("国研智研体 · 诊断")
	if err != nil {
		return err
	}
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	_, err = windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONINFORMATION)
	return err
}
