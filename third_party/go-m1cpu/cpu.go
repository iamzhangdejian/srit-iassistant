//go:build darwin && arm64 && cgo

// Package m1cpu —— 随仓库内置的 go-m1cpu 补丁版（vendored）
//
// 上游 github.com/shoenig/go-m1cpu@v0.1.6 在包初始化阶段执行 `func init() { C.initialize() }`，
// 内部通过 IOKit / CoreFoundation 探测 CPU 主频。在没有 graphics/IOKit 权限的环境
// （本机文件沙箱、无窗口 CI 容器、部分受管终端）里，这一步会直接 SIGSEGV，
// 让进程在 main 之前崩溃，表现为「双击没反应」。
//
// 向量库 SDK（milvus-io/milvus/pkg/v2/util/hardware）为了统计 CPU 主频而间接引入本包，
// 属于「非必要能力」。因此这里用纯 Go 的保守实现替代：主频走 sysctl（不需要 cgo 权限），
// 核心数走 runtime.NumCPU()，取不到就返回 0，绝不 panic、绝不崩溃。
//
// 这是对上游源码的修改版（MPL-2.0，见同目录 LICENSE 与 NOTICE）：
//   - 删除 cgo 初始化与 IOKit 调用；
//   - PCoreHz/ECoreHz/PCoreCache/ECoreCache 返回 0（上游是真实探测值）；
//   - PCoreCount/ECoreCount/ModelName 改用 sysctl，失败退化为 NumCPU。
//
// 影响面：仅影响「CPU 主频/缓存」这类统计展示，不涉及任何业务逻辑。
package m1cpu

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// IsAppleSilicon 在 arm64 darwin 上恒为 true。
func IsAppleSilicon() bool { return true }

// sysctlInt 读取一个整数型 sysctl 键；失败返回 0，绝不 panic。
func sysctlInt(key string) int {
	out, err := exec.Command("sysctl", "-n", key).Output()
	if err != nil {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// PCoreHz 返回性能核主频（Hz）。无法探测时返回 0。
func PCoreHz() uint64 { return 0 }

// ECoreHz 返回能效核主频（Hz）。无法探测时返回 0。
func ECoreHz() uint64 { return 0 }

// PCoreGHz 返回性能核主频（GHz）。
func PCoreGHz() float64 { return float64(PCoreHz()) / 1e9 }

// ECoreGHz 返回能效核主频（GHz）。
func ECoreGHz() float64 { return float64(ECoreHz()) / 1e9 }

// PCoreCount 返回性能核数量；探测失败时退化为 NumCPU/2。
func PCoreCount() int {
	if n := sysctlInt("hw.perflevel0.logicalcpu"); n > 0 {
		return n
	}
	if n := runtime.NumCPU(); n > 1 {
		return n / 2
	}
	return runtime.NumCPU()
}

// ECoreCount 返回能效核数量；探测失败时退化为 NumCPU/2。
func ECoreCount() int {
	if n := sysctlInt("hw.perflevel1.logicalcpu"); n > 0 {
		return n
	}
	if n := runtime.NumCPU(); n > 1 {
		return n / 2
	}
	return runtime.NumCPU()
}

// PCoreCache 返回 P 核缓存 (L1 数据, L1 指令, L2)。探测失败返回 0。
func PCoreCache() (int, int, int) { return 0, 0, 0 }

// ECoreCache 返回 E 核缓存 (L1 数据, L1 指令, L2)。探测失败返回 0。
func ECoreCache() (int, int, int) { return 0, 0, 0 }

// ModelName 返回 CPU 型号名。无法探测时返回空串。
func ModelName() string {
	out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}