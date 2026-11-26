// Package main 是一个「最小可执行体」（命令名 windiag），只为回答一个问题：
// 这台 Windows 到底能不能跑我们编出来的程序？
//
// 它刻意不依赖任何业务代码（只用标准库 + x/sys 的 MessageBox），因此：
//   - 如果它双击能弹出窗口、能写出 diag.txt，说明工具链产物本身没问题，
//     大包打不开的原因在依赖/体积/杀软/业务初始化；
//   - 如果它也毫无反应，那说明问题在产物格式或机器本身（位数、系统版本、拦截）。
//
// 构建：CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/windiag
// （控制台子系统 + 弹窗，双击时既能弹窗，在 cmd 里跑也能看到输出）
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func main() {
	lines := []string{
		"windiag",
		"time: " + time.Now().Format("2006-01-02 15:04:05"),
		fmt.Sprintf("os: %s/%s", runtime.GOOS, runtime.GOARCH),
		"go: " + runtime.Version(),
		"exe: " + mustExecutable(),
		"cwd: " + mustGetwd(),
		"userConfigDir: " + mustUserConfigDir(),
	}
	for _, l := range lines {
		fmt.Println(l)
	}

	written, writeErr := writeDiag(lines)
	switch {
	case writeErr != nil:
		fmt.Println("write diag failed:", writeErr)
	default:
		fmt.Println("diag written:", written)
	}

	message := "windiag 运行正常。\n\n"
	if writeErr != nil {
		message += "但写文件失败：" + writeErr.Error()
	} else {
		message += "诊断文件已写入：\n" + written
	}
	if err := notify(message); err != nil {
		fmt.Println("messagebox failed:", err)
	}
}

// mustExecutable 返回自身的绝对路径，用来确认用户双击的到底是哪个文件。
func mustExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown: " + err.Error()
	}
	if abs, err := filepath.Abs(exe); err == nil {
		return abs
	}
	return exe
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "unknown: " + err.Error()
	}
	return wd
}

// mustUserConfigDir 返回 %APPDATA%——正是国研智研体放日志和数据的根。
func mustUserConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "unknown: " + err.Error()
	}
	return dir
}

// writeDiag 把同样的信息落到 %APPDATA%\国研智研体\logs\diag.txt，
// 路径与桌面端 boot.log 同源，便于用户一次找到全部材料。
func writeDiag(lines []string) (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "国研智研体", "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, "diag.txt")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		return "", err
	}
	return target, nil
}
