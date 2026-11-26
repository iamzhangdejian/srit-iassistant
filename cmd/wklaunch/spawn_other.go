//go:build !windows

package main

import (
	"errors"
	"runtime"
)

// spawn 在非 Windows 上只有一种用途：让 go build ./... 与 go test ./cmd/wklaunch
// 在开发机（macOS / Linux）上照样通过。真正拉起进程的实现在 spawn_windows.go。
//
// 返回明确的错误而不是静默成功，避免有人在 CI 里误以为「启动器验证过了」。
func spawn(plan launchPlan) spawnResult {
	return spawnResult{
		Err: errors.New("wklaunch 只在 Windows 上有意义（当前 " + runtime.GOOS + "）"),
	}
}
