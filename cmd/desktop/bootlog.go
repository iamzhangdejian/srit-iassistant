package main

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"time"
)

// boot 日志是「双击没反应」类问题的唯一取证手段。
//
// Windows 上 Wails 应用没有控制台，启动过程中的 panic 直接把进程带走，用户
// 只看到一个闪过的窗口。而正式的 logger 要等 logger.ConfigureFromEnv() 之后
// 才生效（Windows 早期版本甚至没有日志路径），所以这里在 main 的最前面就写
// 一份最朴素的文件日志，只依赖标准库。
const bootLogFileName = "boot.log"

func bootLogPath(execPath string) string {
	dir, err := desktopStateDir(execPath)
	if err != nil || strings.TrimSpace(dir) == "" {
		if portable := strings.TrimSpace(os.Getenv("WK_PORTABLE_DIR")); portable != "" {
			dir = portable
		} else {
			return ""
		}
	}
	return filepath.Join(dir, "logs", bootLogFileName)
}

func appendBootLog(execPath, format string, args ...any) {
	logPath := bootLogPath(execPath)
	if logPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

// recoverBootPanic 把启动期 panic（含 goroutine 栈）落盘，再把 panic 抛回去，
// 保持原有崩溃语义；文件名里带 boot 便于和正式日志区分。
func recoverBootPanic(execPath string) {
	recovered := recover()
	if recovered == nil {
		return
	}
	appendBootLog(execPath, "PANIC: %v\n%s", recovered, debug.Stack())
	notifyBootFailure(execPath, recovered)
	panic(recovered)
}

func bootLogHeader(execPath string) {
	appendBootLog(execPath, "boot: version=%s os=%s/%s exe=%s",
		desktopVersion, goruntime.GOOS, goruntime.GOARCH, execPath)
}
