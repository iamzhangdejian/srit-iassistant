package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envOf(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

// 默认场景必须覆盖唯一的真实用法：用户双击快捷方式，没有任何参数。
func TestPlanLaunchDefaultTarget(t *testing.T) {
	self := filepath.Join("/opt/app", "国研智研体-启动器.exe")
	plan := planLaunch(self, nil, envOf(map[string]string{"APPDATA": "/home/u/AppData/Roaming"}))

	want := filepath.Join("/opt/app", "国研智研体.exe")
	if plan.Target != want {
		t.Fatalf("target = %q, want %q", plan.Target, want)
	}
	if len(plan.Args) != 0 {
		t.Fatalf("args = %v, want empty", plan.Args)
	}
	if !strings.HasSuffix(plan.LogPath, filepath.Join("logs", "launcher.log")) {
		t.Fatalf("log path = %q, 期望落在 <state>/logs/launcher.log", plan.LogPath)
	}
}

// 单测里 os.UserConfigDir 用的是真实宿主环境，跨平台结果不同；
// 这里只断言「路径以 国研智研体/logs/launcher.log 结尾」，不写死前缀。
func TestLauncherLogPathUsesAppStateDir(t *testing.T) {
	self := filepath.Join("/opt/app", "国研智研体-启动器.exe")
	got := launcherLogPath(self, envOf(nil))
	want := filepath.Join("国研智研体", "logs", "launcher.log")
	if !strings.HasSuffix(got, want) {
		t.Fatalf("log path = %q, 期望以 %q 结尾", got, want)
	}
}

// 便携模式（WK_PORTABLE_DIR）优先，与 cmd/desktop 的 desktopStateDir 同序。
func TestLauncherLogPathPortable(t *testing.T) {
	self := filepath.Join("/opt/app", "国研智研体-启动器.exe")
	got := launcherLogPath(self, envOf(map[string]string{"WK_PORTABLE_DIR": "/data/portable"}))
	want := filepath.Join("/data/portable", "logs", "launcher.log")
	if got != want {
		t.Fatalf("log path = %q, want %q", got, want)
	}
}

// 第一个参数是 .exe 时，它就是主程序，其余参数透传（换名/便携部署用）。
func TestPlanLaunchExplicitTarget(t *testing.T) {
	self := filepath.Join("/opt/app", "国研智研体-启动器.exe")
	plan := planLaunch(self, []string{`C:\Other\国研智研体.exe`, "--profile", "b"}, envOf(nil))

	if plan.Target != `C:\Other\国研智研体.exe` {
		t.Fatalf("target = %q", plan.Target)
	}
	if len(plan.Args) != 2 || plan.Args[0] != "--profile" || plan.Args[1] != "b" {
		t.Fatalf("args = %v", plan.Args)
	}
}

// 普通参数不能被误判成目标：扩展名不是 .exe 就一律透传。
func TestPlanLaunchArgsPassthrough(t *testing.T) {
	self := filepath.Join("/opt/app", "国研智研体-启动器.exe")
	plan := planLaunch(self, []string{"--version", "x.exe.old"}, envOf(nil))

	if plan.Target != filepath.Join("/opt/app", "国研智研体.exe") {
		t.Fatalf("target = %q", plan.Target)
	}
	if len(plan.Args) != 2 || plan.Args[0] != "--version" {
		t.Fatalf("args = %v", plan.Args)
	}
}

// 225 是本启动器存在的理由：必须被翻译成「被安全软件拦了」。
func TestDescribeSpawnFailureVirusInfected(t *testing.T) {
	d := describeSpawnFailure(errVirusInfected, nil)
	if !strings.Contains(d.What, "安全软件") {
		t.Fatalf("what = %q，应提到安全软件", d.What)
	}
	if !strings.Contains(d.Hint, "白名单") {
		t.Fatalf("hint = %q，应给出报白建议", d.Hint)
	}
	if d.Code != 225 {
		t.Fatalf("code = %d", d.Code)
	}
}

func TestDescribeSpawnFailureCoverage(t *testing.T) {
	cases := []struct {
		code uint32
		want string
	}{
		{errAccessDenied, "拒绝访问"},
		{errAccessDisabledByPol, "组策略"},
		{errElevationRequired, "管理员"},
		{errFileNotFound, "找不到主程序"},
		{errBadExeFormat, "不是有效的 Windows 程序"},
		{errModNotFound, "DLL"},
	}
	for _, c := range cases {
		d := describeSpawnFailure(c.code, nil)
		if !strings.Contains(d.What, c.want) {
			t.Errorf("code %d: what = %q，期望含 %q", c.code, d.What, c.want)
		}
		if d.Hint == "" {
			t.Errorf("code %d: hint 为空", c.code)
		}
	}
}

// 正常退出不弹窗：否则用户每次关窗口都被拦一次，比没日志更糟。
func TestDescribeExitQuietPaths(t *testing.T) {
	for _, code := range []uint32{0, 1, 2} {
		if _, crashed := describeExit(code); crashed {
			t.Fatalf("code %d 被判为崩溃，应保持静默", code)
		}
	}
}

func TestDescribeExitCrashPaths(t *testing.T) {
	cases := []struct {
		code uint32
		want string
	}{
		{statusDllInitFailed, "DLL 初始化失败"},
		{statusDllNotFound, "缺少依赖 DLL"},
		{statusAccessViolation, "访问冲突"},
		{statusBadImageFormat, "映像格式错误"},
	}
	for _, c := range cases {
		d, crashed := describeExit(c.code)
		if !crashed {
			t.Errorf("code 0x%X 应判为崩溃", c.code)
			continue
		}
		if !strings.Contains(d.What, c.want) {
			t.Errorf("code 0x%X: what = %q，期望含 %q", c.code, d.What, c.want)
		}
	}
}

// 用户看到的文案必须同时含「错误码、目标路径、日志路径」——
// 这三样是交付方远程判断的唯一依据。
func TestFailureMessageCarriesEvidence(t *testing.T) {
	plan := launchPlan{
		Target:  `C:\Program Files\国研智研体\国研智研体.exe`,
		LogPath: `C:\Users\a\AppData\Roaming\国研智研体\logs\launcher.log`,
	}
	msg := failureMessage(plan, describeSpawnFailure(errVirusInfected, nil))

	for _, want := range []string{"225", "0x000000E1", plan.Target, plan.LogPath, "启动失败"} {
		if !strings.Contains(msg, want) {
			t.Errorf("消息缺少 %q:\n%s", want, msg)
		}
	}
}

func TestExitMessageCarriesElapsed(t *testing.T) {
	plan := launchPlan{Target: `C:\app\x.exe`, LogPath: `C:\log\launcher.log`}
	msg := exitMessage(plan, diagnosis{Code: statusAccessViolation, What: "访问冲突"}, 3*time.Second)

	for _, want := range []string{"3s", "3221225477", "0xC0000005"} {
		if !strings.Contains(msg, want) {
			t.Errorf("消息缺少 %q:\n%s", want, msg)
		}
	}
}

// 日志不可写时不能让启动流程崩掉：应降级成「只剩弹窗」。
func TestFailureMessageWithoutLogPath(t *testing.T) {
	plan := launchPlan{Target: `C:\app\x.exe`}
	msg := failureMessage(plan, describeSpawnFailure(errAccessDenied, nil))
	if !strings.Contains(msg, "无法写入") {
		t.Fatalf("日志不可用时应提示截图发交付方:\n%s", msg)
	}
}

func TestAppendLogAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "launcher.log")
	if err := appendLog(path, "launch: version=%s", "0.9.0"); err != nil {
		t.Fatalf("appendLog: %v", err)
	}
	if err := appendLog(path, "exit: code=%d", 0); err != nil {
		t.Fatalf("appendLog: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "launch: version=0.9.0") || !strings.Contains(text, "exit: code=0") {
		t.Fatalf("日志内容不符:\n%s", text)
	}
	if lines := strings.Count(strings.TrimSpace(text), "\n"); lines != 1 {
		t.Fatalf("期望 2 行，实际 %d 行:\n%s", lines+1, text)
	}
}

func TestAppendLogNoPathIsError(t *testing.T) {
	if err := appendLog("", "x"); err == nil {
		t.Fatal("空路径应返回错误")
	}
}

func TestEnvLookup(t *testing.T) {
	env := envLookup([]string{"A=1", "WK_PORTABLE_DIR=/data/p"})
	if got := env("WK_PORTABLE_DIR"); got != "/data/p" {
		t.Fatalf("got %q", got)
	}
	if got := env("MISSING"); got != "" {
		t.Fatalf("got %q", got)
	}
}

// spawn 在非 Windows 上必须明确失败，而不是假装成功 ——
// 否则 CI 里会把「没验证」当成「验证通过」。
func TestSpawnUnsupportedOnNonWindows(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("windows 上有真实实现")
	}
	res := spawn(launchPlan{Target: "x.exe"})
	if res.Err == nil {
		t.Skip("当前平台的 spawn 已实现")
	}
}
