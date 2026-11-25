//go:build licadmin_native && darwin

// macOS 主菜单：系统快捷键的来源。
//
// 为什么要自己装菜单：⌘Q / ⌘W / ⌘M / ⌘C / ⌘V 这些不是窗口自己处理的，
// 而是 NSApplication 先查主菜单（NSApp.mainMenu）里有没有匹配的 keyEquivalent，
// 有才派发给对应的 action。webview_go 只创建窗口、不创建菜单，所以窗口能显示，
// 但按 ⌘Q 毫无反应——用户的第一反应是「这个 app 是坏的」。
//
// 另外编辑菜单不是可选项：没有 Edit 菜单时，WKWebView 里的输入框拿不到 ⌘C/⌘V，
// 而管理端到处都是输入框（发码、令牌、搜索），这属于功能性缺陷而不是观感问题。
package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

@interface LAMenuTarget : NSObject
@end

static LAMenuTarget *gLATarget = nil;

@implementation LAMenuTarget

// 退出走「关窗」而不是 terminate：关窗会让 webview 的 Run() 返回，
// 于是 appmode 里那条「原生窗口已关闭，正在停止服务」的正常收尾路径能跑到。
// 关不掉时再兜底 terminate，避免出现按了 ⌘Q 却没退出的假死观感。
- (void)laQuit:(id)sender {
  NSWindow *w = [NSApp keyWindow];
  if (!w) { w = [[NSApp windows] firstObject]; }
  if (w) { [w performClose:nil]; }
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 400ull * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
    [NSApp terminate:nil];
  });
}

- (void)laClose:(id)sender {
  NSWindow *w = [NSApp keyWindow];
  if (!w) { w = [[NSApp windows] firstObject]; }
  if (w) { [w performClose:nil]; }
}

- (void)laMinimize:(id)sender {
  NSWindow *w = [NSApp keyWindow];
  if (!w) { w = [[NSApp windows] firstObject]; }
  if (w) { [w performMiniaturize:nil]; }
}

- (void)laZoom:(id)sender {
  NSWindow *w = [NSApp keyWindow];
  if (!w) { w = [[NSApp windows] firstObject]; }
  if (w) { [w zoom:nil]; }
}

@end

static NSMenuItem *la_add(NSMenu *menu, NSString *title, SEL action, id target, NSString *key, NSUInteger mods) {
  NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
  if (mods) { [it setKeyEquivalentModifierMask:mods]; }
  if (target) { [it setTarget:target]; }
  [menu addItem:it];
  return it;
}

static int la_install_menu(void) {
  @autoreleasepool {
    if (!gLATarget) { gLATarget = [[LAMenuTarget alloc] init]; }

    NSMenu *mainMenu = [[NSMenu alloc] initWithTitle:@"MainMenu"];

    // —— 应用菜单 ——
    NSMenuItem *appItem = [[NSMenuItem alloc] initWithTitle:@"" action:nil keyEquivalent:@""];
    [mainMenu addItem:appItem];
    NSMenu *appMenu = [[NSMenu alloc] initWithTitle:@"国研智研体授权管理"];
    [appItem setSubmenu:appMenu];
    la_add(appMenu, @"关于 国研智研体授权管理", @selector(orderFrontStandardAboutPanel:), NSApp, @"", 0);
    [appMenu addItem:[NSMenuItem separatorItem]];
    la_add(appMenu, @"隐藏 国研智研体授权管理", @selector(hide:), NSApp, @"h", NSEventModifierFlagCommand);
    la_add(appMenu, @"隐藏其他", @selector(hideOtherApplications:), NSApp, @"h",
           NSEventModifierFlagCommand | NSEventModifierFlagOption);
    la_add(appMenu, @"显示全部", @selector(unhideAllApplications:), NSApp, @"", 0);
    [appMenu addItem:[NSMenuItem separatorItem]];
    la_add(appMenu, @"退出 国研智研体授权管理", @selector(laQuit:), gLATarget, @"q", NSEventModifierFlagCommand);

    // —— 编辑菜单（WKWebView 的 ⌘C/⌘V 靠它派发）——
    NSMenuItem *editItem = [[NSMenuItem alloc] initWithTitle:@"" action:nil keyEquivalent:@""];
    [mainMenu addItem:editItem];
    NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"编辑"];
    [editItem setSubmenu:editMenu];
    la_add(editMenu, @"撤销", @selector(undo:), nil, @"z", NSEventModifierFlagCommand);
    la_add(editMenu, @"重做", @selector(redo:), nil, @"z", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    [editMenu addItem:[NSMenuItem separatorItem]];
    la_add(editMenu, @"剪切", @selector(cut:), nil, @"x", NSEventModifierFlagCommand);
    la_add(editMenu, @"拷贝", @selector(copy:), nil, @"c", NSEventModifierFlagCommand);
    la_add(editMenu, @"粘贴", @selector(paste:), nil, @"v", NSEventModifierFlagCommand);
    la_add(editMenu, @"全选", @selector(selectAll:), nil, @"a", NSEventModifierFlagCommand);

    // —— 窗口菜单 ——
    NSMenuItem *winItem = [[NSMenuItem alloc] initWithTitle:@"" action:nil keyEquivalent:@""];
    [mainMenu addItem:winItem];
    NSMenu *winMenu = [[NSMenu alloc] initWithTitle:@"窗口"];
    [winItem setSubmenu:winMenu];
    la_add(winMenu, @"最小化", @selector(laMinimize:), gLATarget, @"m", NSEventModifierFlagCommand);
    la_add(winMenu, @"缩放", @selector(laZoom:), gLATarget, @"", 0);
    [winMenu addItem:[NSMenuItem separatorItem]];
    la_add(winMenu, @"关闭窗口", @selector(laClose:), gLATarget, @"w", NSEventModifierFlagCommand);

    [NSApp setMainMenu:mainMenu];
    [NSApp setWindowsMenu:winMenu];

    int n = 0;
    for (NSMenuItem *top in [mainMenu itemArray]) {
      n += 1 + (int)[[[top submenu] itemArray] count];
    }
    return n;
  }
}

// la_find_key 递归查主菜单里有没有「这个键 + 这些修饰键」的条目。
//
// 为什么要查：快捷键能不能用，完全取决于 NSApp.mainMenu 里能不能找到匹配的
// keyEquivalent——不是窗口自己监听键盘。装完当场回查一遍，才能把「我以为装了」
// 和「系统真的认得」区分开，日志里也就有了可核对的证据。
static BOOL la_find_key(NSMenu *menu, NSString *key, NSEventModifierFlags mods) {
  for (NSMenuItem *it in [menu itemArray]) {
    if ([[it keyEquivalent] isEqualToString:key]) {
      NSEventModifierFlags m = [it keyEquivalentModifierMask] & NSEventModifierFlagDeviceIndependentFlagsMask;
      if (m == (mods & NSEventModifierFlagDeviceIndependentFlagsMask)) { return YES; }
    }
    if ([it submenu] && la_find_key([it submenu], key, mods)) { return YES; }
  }
  return NO;
}

// la_check_shortcuts 返回位掩码：1=⌘Q 2=⌘W 4=⌘M 8=⌘C 16=⌘V
static int la_check_shortcuts(void) {
  int mask = 0;
  NSMenu *main = [NSApp mainMenu];
  if (!main) { return 0; }
  if (la_find_key(main, @"q", NSEventModifierFlagCommand)) { mask |= 1; }
  if (la_find_key(main, @"w", NSEventModifierFlagCommand)) { mask |= 2; }
  if (la_find_key(main, @"m", NSEventModifierFlagCommand)) { mask |= 4; }
  if (la_find_key(main, @"c", NSEventModifierFlagCommand)) { mask |= 8; }
  if (la_find_key(main, @"v", NSEventModifierFlagCommand)) { mask |= 16; }
  return mask;
}
*/
import "C"

// installNativeMenu 装主菜单并返回装进去的菜单项数量（含顶层与子项），
// 数量为 0 说明 AppKit 没接住，日志里能直接看出来。
func installNativeMenu() int {
	return int(C.la_install_menu())
}

// menuShortcutMask 回查主菜单里实际认得的快捷键，供启动日志核对。
func menuShortcutMask() int {
	return int(C.la_check_shortcuts())
}
