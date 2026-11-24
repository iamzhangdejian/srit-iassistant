//go:build !bindings

package main

import (
	_ "embed"
	"strings"
)

// versionFile 是编译期嵌入到二进制里的版本号。
//
// 为什么要 embed 而不是运行时读文件：
//   - macOS .app 启动后 os.Chdir 会切到 Contents/Resources，那里原本没有
//     VERSION 文件，desktopAboutVersion() 的三级相对路径全部落空 → 返回
//     "unknown" → 标题栏退化成只有应用名。
//   - 把版本号打进二进制后，产物自带版本信息，用户不管从哪个目录启动、
//     不管有没有 VERSION 文件都能正确显示。
//
// 同步方式：scripts/oem-build-lite.sh 与 package-*.sh 在构建前把仓库根的
// VERSION 复制到本文件旁边的 VERSION.embedded；手工构建时若忘记同步，
// desktopWindowTitle() 会回退到 desktopAboutVersion()，不会报错。
//
//go:embed VERSION.embedded
var versionFile string

// version 由构建脚本通过 -ldflags "-X 'main.version=0.9.0'" 注入，
// 优先级最高：手工 wails build 忘了带 ldflags 时才回退到嵌入的 VERSION.embedded。
//
// 注意：不能用 const，ldflags -X 只对包级 string 变量生效。
var version string

// desktopVersion 是整理后的版本号，供标题栏与关于对话框使用。
var desktopVersion = func() string {
	if v := strings.TrimSpace(version); v != "" && v != "unknown" {
		return v
	}
	return strings.TrimSpace(versionFile)
}()
