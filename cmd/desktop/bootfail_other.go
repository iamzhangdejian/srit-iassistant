//go:build !windows

package main

// notifyBootFailure 在非 Windows 平台不弹窗：终端会直接打印 panic，
// 启动日志也已经落到 boot.log。
func notifyBootFailure(string, any) {}
