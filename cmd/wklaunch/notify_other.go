//go:build !windows

package main

// notify 在非 Windows 上只打到 stdout，让同一个包也能在开发机上编译与测试。
func notify(message string, failure bool) error {
	_ = failure
	println(message)
	return nil
}
