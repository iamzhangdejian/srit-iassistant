// Package main 实现国研智研体在 Windows 上的启动器（命令名 wklaunch）。
//
// 为什么需要它：主程序是 ~242MB、带 cgo 与静态 Rust 库的 GUI 可执行文件。
// 在企业终端管控 / 第三方安全软件环境里，它可能在 CreateProcess 的那一刻就被
// 拦下 —— 进程根本没被创建，于是主程序自己的 boot.log 一行都不会有，用户看到
// 的就是「双击没反应，也没有任何错误日志」。这种情况下应用内部无论加多少日志
// 都救不了，因为「应用」根本没开始运行。
//
// 启动器只做三件事：
//  1. 先写 launcher.log —— 比主程序更早，且不依赖任何业务代码；
//  2. CreateProcess 主程序，命令行参数原样透传；
//  3. 拉起失败时把 Win32 错误码翻译成一句人话，落盘并弹窗；成功则静默等待。
//
// 它刻意只用标准库 + x/sys（CGO_ENABLED=0），所以：
//   - 在 macOS 上就能交叉编译出真 exe（GOOS=windows GOARCH=amd64 go build），
//     与 cmd/windiag 同一条路子，不必等 CI 才能验证；
//   - 它自己被安全软件拦掉的概率远低于 242MB 的主程序，能把「谁被拦了」分开。
//
// 构建（CI 与本地同一条命令）：
//
//	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
//	  -ldflags "-s -w -H windowsgui -X main.version=$VERSION" ./cmd/wklaunch
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// version 由构建时注入（-X main.version=0.9.0）；本地 go run 时是 dev。
var version = "dev"

const (
	// appName 必须与 cmd/desktop/main.go 的 desktopAppName 一致：
	// 启动器要和主程序写同一个 %APPDATA% 子目录，用户才能一次找齐全部日志。
	appName = "国研智研体"

	// defaultTargetName 是安装目录里主程序的文件名（NSIS 用 AppName.exe 落盘）。
	defaultTargetName = appName + ".exe"

	// launcherLogFileName 与 boot.log 同目录，刻意分开命名，
	// 这样「启动器确实跑了、但主程序没起来」和「启动器也没跑」能区分开。
	launcherLogFileName = "launcher.log"
)

func main() {
	os.Exit(run(os.Args[1:], os.Environ()))
}

// run 是 main 的可测骨架：不读全局状态，参数与失败都显式传进传出。
func run(args []string, environ []string) int {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		self = "wklaunch.exe"
	}

	plan := planLaunch(self, args, envLookup(environ))
	logf := func(format string, a ...any) {
		_ = appendLog(plan.LogPath, format, a...)
	}

	logf("launch: version=%s os=%s/%s exe=%s", version, runtime.GOOS, runtime.GOARCH, plan.Self)
	logf("target: %s exists=%t size=%s", plan.Target, fileExists(plan.Target), fileSize(plan.Target))
	if len(plan.Args) > 0 {
		logf("args: %q", plan.Args)
	}
	if plan.LogPath == "" {
		// 不阻断启动：日志写不出来只是取证能力下降，主程序该跑还得跑。
		logf("warn: 无法确定日志路径")
	}

	res := spawn(plan)
	if res.Err != nil {
		d := describeSpawnFailure(res.Win32Code, res.Err)
		logf("spawn: failed code=%d (0x%08X) %s", d.Code, d.Code, d.What)
		_ = notify(failureMessage(plan, d), true)
		return 1
	}

	logf("spawn: ok pid=%d", res.PID)
	logf("exit: code=%d (0x%08X) elapsed=%s", res.ExitCode, res.ExitCode, res.Elapsed.Round(time.Millisecond))

	// 主程序正常退出不打扰用户；只有「看着像崩溃」的退出码才弹窗。
	// 否则每次关窗口都弹一次，比没日志更烦。
	if d, crashed := describeExit(res.ExitCode); crashed {
		_ = notify(exitMessage(plan, d, res.Elapsed), true)
	}
	return int(res.ExitCode)
}

// spawnResult 是一次 CreateProcess 的完整结果。
// Win32Code 只在 Err != nil 时有意义，用来做「错误码 → 人话」的翻译。
// 放在这里而不是 spawn_windows.go：非 Windows 的桩实现也要返回它。
type spawnResult struct {
	PID       uint32
	ExitCode  uint32
	Elapsed   time.Duration
	Err       error
	Win32Code uint32
}

// launchPlan 是一次启动的全部输入，独立于操作系统，便于单测。
type launchPlan struct {
	Self    string   // 启动器自身路径
	Target  string   // 主程序路径
	Args    []string // 透传给主程序的参数（不含自身路径与 target）
	LogPath string   // 日志文件路径；空串表示无法确定
}

// planLaunch 决定「拉起谁、带什么参数、日志写哪」。
//
// 目标解析规则（保持简单可预测，不做模糊猜测）：
//   - 第一个参数以 .exe 结尾 → 它就是主程序，其余参数透传（便于换名/便携部署）；
//   - 否则一律拉起同目录下的 国研智研体.exe，全部参数透传。
//
// 默认行为必须覆盖唯一真实场景：用户双击开始菜单快捷方式（无参数）。
func planLaunch(self string, args []string, env func(string) string) launchPlan {
	dir := filepath.Dir(self)
	plan := launchPlan{
		Self:   self,
		Target: filepath.Join(dir, defaultTargetName),
		Args:   append([]string(nil), args...),
	}
	if len(args) > 0 && strings.EqualFold(filepath.Ext(args[0]), ".exe") {
		plan.Target = args[0]
		plan.Args = append([]string(nil), args[1:]...)
	}
	plan.LogPath = launcherLogPath(self, env)
	return plan
}

// launcherLogPath 与 cmd/desktop/bootlog.go 的 bootLogPath 保持同一答案：
// 先看 WK_PORTABLE_DIR，再走 %APPDATA%\国研智研体。两处逻辑刻意各写一份 ——
// 启动器不能依赖业务代码，否则业务代码一坏，取证手段也跟着坏。
func launcherLogPath(self string, env func(string) string) string {
	if dir := stateDir(env); dir != "" {
		return filepath.Join(dir, "logs", launcherLogFileName)
	}
	// 兜底：写在启动器旁边。安装目录通常只读，写不进去时 appendLog 会静默失败，
	// 此时只剩弹窗这一条证据通道。
	if dir := filepath.Dir(self); dir != "" && dir != "." {
		return filepath.Join(dir, launcherLogFileName)
	}
	return ""
}

func stateDir(env func(string) string) string {
	if portable := strings.TrimSpace(env("WK_PORTABLE_DIR")); portable != "" {
		return portable
	}
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		return ""
	}
	return filepath.Join(configDir, appName)
}

// appendLog 追加一行到日志；任何错误都吞掉并返回给调用方（调用方一般忽略）。
// 这里不写 stderr 是刻意的：GUI 子系统没有控制台，写了也没人看见。
func appendLog(path, format string, args ...any) error {
	if path == "" {
		return fmt.Errorf("no log path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	return err
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func fileSize(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", info.Size())
}

// failureMessage 是用户真正看到的那段话。它必须同时给出三样东西：
// 一句人话、一个可以交给 IT 的错误码、日志在哪。
func failureMessage(plan launchPlan, d diagnosis) string {
	var b strings.Builder
	b.WriteString("国研智研体启动失败。\n\n")
	b.WriteString("主程序没能被拉起：" + d.What + "\n")
	if d.Code != 0 {
		b.WriteString(fmt.Sprintf("错误码：%d (0x%08X)\n", d.Code, d.Code))
	}
	if d.Hint != "" {
		b.WriteString("建议：" + d.Hint + "\n")
	}
	b.WriteString("\n主程序：" + plan.Target)
	b.WriteString("\n日志：" + logLocation(plan))
	return b.String()
}

// exitMessage 用于「进程起来了，但随即异常退出」—— 这时的错误码是 NTSTATUS。
func exitMessage(plan launchPlan, d diagnosis, elapsed time.Duration) string {
	var b strings.Builder
	b.WriteString("国研智研体异常退出。\n\n")
	b.WriteString("主程序启动后 " + elapsed.Round(time.Second).String() + " 内退出：" + d.What + "\n")
	if d.Code != 0 {
		b.WriteString(fmt.Sprintf("退出码：%d (0x%08X)\n", d.Code, d.Code))
	}
	if d.Hint != "" {
		b.WriteString("建议：" + d.Hint + "\n")
	}
	b.WriteString("\n日志：" + logLocation(plan))
	return b.String()
}

func logLocation(plan launchPlan) string {
	if plan.LogPath == "" {
		return "（无法写入，请把本窗口内容截图发给交付方）"
	}
	return plan.LogPath
}

// envLookup 把 []string 形式的 environ 包成查表函数，避免单测碰真实环境变量。
func envLookup(environ []string) func(string) string {
	return func(key string) string {
		prefix := key + "="
		for _, kv := range environ {
			if strings.HasPrefix(kv, prefix) {
				return strings.TrimPrefix(kv, prefix)
			}
		}
		return ""
	}
}
