//go:build !windows

package main

// notify 在非 Windows 上不弹窗，只把内容打到 stdout，
// 这样同一个文件也能在开发机上 go vet / go build 通过。
func notify(message string) error {
	println(message)
	return nil
}
