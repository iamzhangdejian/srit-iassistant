//go:build windows

package main

import (
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// spawn 拉起主程序并等它退出。
//
// 两个刻意的选择：
//   - 继承句柄传 false：主程序是 GUI 应用，不需要继承我们的句柄；
//   - 工作目录设成主程序所在目录：与快捷方式「起始位置」的默认行为一致，
//     保证「双击快捷方式」和「双击 exe」看到的相对路径相同。
func spawn(plan launchPlan) spawnResult {
	start := time.Now()
	var res spawnResult

	cmdline := plan.Target
	if len(plan.Args) > 0 {
		cmdline = windows.ComposeCommandLine(append([]string{plan.Target}, plan.Args...))
	}

	cmdPtr, err := windows.UTF16PtrFromString(cmdline)
	if err != nil {
		res.Err = err
		return res
	}
	dirPtr, err := windows.UTF16PtrFromString(filepath.Dir(plan.Target))
	if err != nil {
		res.Err = err
		return res
	}

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation

	// appName 传 nil，让命令行第一段决定可执行文件 —— 这样带空格的中文路径
	// 由 ComposeCommandLine 负责加引号，比自己拼字符串可靠。
	if err := windows.CreateProcess(nil, cmdPtr, nil, nil, false, 0, nil, dirPtr, &si, &pi); err != nil {
		res.Err = err
		res.Win32Code = win32Code(err)
		return res
	}
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)

	res.PID = pi.ProcessId
	_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)

	var code uint32
	if err := windows.GetExitCodeProcess(pi.Process, &code); err != nil {
		res.Err = err
		res.Win32Code = win32Code(err)
		return res
	}

	res.ExitCode = code
	res.Elapsed = time.Since(start)
	return res
}

// win32Code 从 x/sys 的错误里取出原始数字。
// 无法识别时返回 0，让诊断表走到 default 分支（仍会把 err.Error() 打进去）。
func win32Code(err error) uint32 {
	if err == nil {
		return 0
	}
	if errno, ok := err.(windows.Errno); ok {
		return uint32(errno)
	}
	return 0
}
