//go:build licadmin_native && !darwin

// 非 macOS 平台没有 NSApplication 主菜单这一层，快捷键由窗口系统自己处理，
// 这里给一个空实现，保证 licadmin_native 标签在别的平台上仍能编译。
package main

func installNativeMenu() int { return 0 }

func menuShortcutMask() int { return 0 }
