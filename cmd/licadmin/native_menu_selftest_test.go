//go:build licadmin_native && darwin && licadmin_menu_selftest

// 快捷键的端到端自检。
//
// 为什么放在 TestMain：AppKit 要求在主线程创建 NSApplication 与窗口，
// 而 go test 只在主协程里调 TestMain，普通 Test 跑在别的线程上（会直接段错误）。
//
// 检查两件事：主菜单里认不认 ⌘M；把 ⌘M 交给 -[NSMenu performKeyEquivalent:]
// 之后事件是否被认领、窗口是否真的最小化。这和用户按键时 NSApplication.sendEvent
// 走的是同一条派发路径，不需要辅助功能权限。
package main

import (
	"os"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	runtime.LockOSThread()

	menuTestAppInit()
	menuTestWindow()

	n := installNativeMenu()
	if n == 0 {
		println("自检失败：主菜单没有装上")
		os.Exit(1)
	}
	if mask := menuShortcutMask(); mask&4 == 0 {
		println("自检失败：主菜单里找不到 ⌘M，mask=", mask)
		os.Exit(1)
	}
	println("自检：菜单项", n, "个，快捷键回查", describeShortcutMask(menuShortcutMask()))

	if !menuTestPress("m") {
		println("自检失败：⌘M 没有被主菜单认领")
		os.Exit(1)
	}
	menuTestSpin(0.5)
	if !menuTestMinimized() {
		println("自检失败：⌘M 被认领了但窗口没有最小化")
		os.Exit(1)
	}
	println("自检通过：⌘M 被主菜单认领，窗口已最小化")

	os.Exit(m.Run())
}
