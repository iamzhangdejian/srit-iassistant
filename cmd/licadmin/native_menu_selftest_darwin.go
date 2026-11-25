//go:build licadmin_native && darwin && licadmin_menu_selftest

// 快捷键自检用的测试钩子（只在显式加 licadmin_menu_selftest 标签时编入，
// 交付包不含这段代码）。Go 不允许在 _test.go 里用 cgo，所以把 Objective-C
// 这一层放在普通文件里，测试文件只调这几个 Go 函数。
package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

static void la_hook_app_init(void) {
  [NSApplication sharedApplication];
  [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
}

static void la_hook_window(void) {
  NSWindow *w = [[NSWindow alloc]
      initWithContentRect:NSMakeRect(0, 0, 480, 320)
                styleMask:(NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
                           NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable)
                  backing:NSBackingStoreBuffered
                    defer:NO];
  [w makeKeyAndOrderFront:nil];
}

// la_hook_press 合成一次带 ⌘ 的按键，走主菜单的 performKeyEquivalent 派发——
// 这正是用户按键时 NSApplication.sendEvent 走的那条路径，不需要辅助功能权限。
static int la_hook_press(const char *key) {
  NSString *k = [NSString stringWithUTF8String:key];
  NSWindow *w = [NSApp keyWindow];
  NSEvent *ev = [NSEvent keyEventWithType:NSEventTypeKeyDown
                                location:NSZeroPoint
                           modifierFlags:NSEventModifierFlagCommand
                               timestamp:0
                            windowNumber:(w ? [w windowNumber] : 0)
                                 context:nil
                              characters:k
             charactersIgnoringModifiers:k
                               isARepeat:NO
                                 keyCode:0];
  return [[NSApp mainMenu] performKeyEquivalent:ev] ? 1 : 0;
}

// 看「有没有任何一个窗口被最小化」，而不是只看 keyWindow：
// 测试进程没被激活时 keyWindow 常常是 nil，只看它会把成功误判成失败。
static int la_hook_minimized(void) {
  for (NSWindow *w in [NSApp windows]) {
    if ([w isMiniaturized]) { return 1; }
  }
  return 0;
}

static void la_hook_spin(double seconds) {
  [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:seconds]];
}
*/
import "C"

import "unsafe"

func menuTestAppInit() { C.la_hook_app_init() }

func menuTestWindow() { C.la_hook_window() }

func menuTestPress(key string) bool {
	cs := C.CString(key)
	defer C.free(unsafe.Pointer(cs))
	return C.la_hook_press(cs) != 0
}

func menuTestMinimized() bool { return C.la_hook_minimized() != 0 }

func menuTestSpin(seconds float64) { C.la_hook_spin(C.double(seconds)) }
