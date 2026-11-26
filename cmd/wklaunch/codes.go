package main

import "fmt"

// diagnosis 是一条「错误码 → 人话」的翻译结果。
//
// 这里全部是纯数据与纯函数，不带 build tag：Windows 上跑得到，
// macOS / Linux 上也能被 go test 覆盖（启动器的取证逻辑必须可本地验证）。
type diagnosis struct {
	Code uint32 // 原始错误码（Win32 error 或 NTSTATUS）
	What string // 一句话原因
	Hint string // 用户/IT 的下一步
}

// 这些常量把「魔法数字」变成可 grep 的名字。
const (
	errFileNotFound        = 2
	errPathNotFound        = 3
	errAccessDenied        = 5
	errSharingViolation    = 32
	errModNotFound         = 126
	errBadExeFormat        = 193
	errExeMachineType      = 216
	errVirusInfected       = 225
	errElevationRequired   = 740
	errAccessDisabledByPol = 1260
	errFileCorrupt         = 1392
	errNoSystemResources   = 1450
)

// NTSTATUS：进程确实起来了，但加载/初始化阶段就死了 —— 退出码落在这个区间。
const (
	statusAccessViolation    = 0xC0000005
	statusIllegalInstruction = 0xC000001D
	statusBadImageFormat     = 0xC000007B
	statusStackOverflow      = 0xC00000FD
	statusDllNotFound        = 0xC0000135
	statusEntryPointNotFound = 0xC0000139
	statusDllInitFailed      = 0xC0000142
	statusFailFast           = 0xC0000409
)

// describeSpawnFailure 翻译 CreateProcess 的失败原因。
//
// 225(ERROR_VIRUS_INFECTED) 与 1260(ERROR_ACCESS_DISABLED_BY_POLICY) 是
// 本启动器存在的全部理由：它们是「安全软件/组策略拦掉了」的直接证据，
// 而在没有启动器之前，这个信息永远不会出现在任何日志里。
func describeSpawnFailure(code uint32, err error) diagnosis {
	d := diagnosis{Code: code}
	switch code {
	case errVirusInfected:
		d.What = "被安全软件判定为病毒/威胁并阻止"
		d.Hint = "把安装目录（不是单个文件）加入安全软件白名单后重试；升级后路径不变，报白长期有效"
	case errAccessDisabledByPol:
		d.What = "被组策略禁止运行"
		d.Hint = "联系客户 IT 在「软件限制策略 / AppLocker」中放行安装目录下的程序"
	case errAccessDenied:
		d.What = "拒绝访问（权限或安全策略）"
		d.Hint = "确认安装目录可读、程序未被安全软件隔离；必要时用管理员身份运行"
	case errElevationRequired:
		d.What = "需要管理员权限才能运行"
		d.Hint = "右键以管理员身份运行，或检查程序清单里的 requestedExecutionLevel"
	case errFileNotFound:
		d.What = "找不到主程序文件"
		d.Hint = "确认安装目录里有 " + defaultTargetName + "；若文件不存在，多半是安装/复制阶段就被拦掉了"
	case errPathNotFound:
		d.What = "安装目录不存在"
		d.Hint = "重新运行安装包；若安装成功仍报此错，检查是否被安全软件回滚"
	case errBadExeFormat:
		d.What = "不是有效的 Windows 程序（文件损坏或格式不对）"
		d.Hint = "核对文件大小与交付方给出的哈希；不一致请重新拷贝/重新安装"
	case errExeMachineType:
		d.What = "程序位数与系统不匹配"
		d.Hint = "amd64 包只能装在 64 位 Windows 上"
	case errModNotFound:
		d.What = "缺少依赖 DLL"
		d.Hint = "确认安装目录里带着 libstdc++-6.dll 等运行库，或被安全软件删除了"
	case errSharingViolation:
		d.What = "文件被占用"
		d.Hint = "上一个实例还没退出，或安全软件正在扫描；稍后重试"
	case errFileCorrupt:
		d.What = "文件已损坏"
		d.Hint = "重新安装；若反复损坏，检查磁盘与安全软件"
	case errNoSystemResources:
		d.What = "系统资源不足"
		d.Hint = "关闭其他程序后重试"
	default:
		d.What = "无法创建进程"
		if err != nil {
			d.What += "：" + err.Error()
		}
		d.Hint = "把本窗口截图与日志一起发给交付方"
	}
	return d
}

// describeExit 翻译退出码，第二个返回值表示「是否算异常退出」。
//
// 判定故意保守：只有 NTSTATUS 的严重错误区间（>= 0xC0000000）和
// 少量明确的失败码才算崩溃。0 与普通非零退出码一律不弹窗 ——
// 用户正常关窗口时如果弹窗，比没有日志更让人困惑。
func describeExit(code uint32) (diagnosis, bool) {
	if code == 0 {
		return diagnosis{Code: code, What: "正常退出"}, false
	}
	if code < 0xC0000000 {
		return diagnosis{Code: code, What: "以非零码退出"}, false
	}
	d := diagnosis{Code: code}
	switch code {
	case statusAccessViolation:
		d.What = "访问冲突（内存越界或空指针）"
		d.Hint = "把日志发给交付方定位；这不是环境问题"
	case statusDllInitFailed:
		d.What = "DLL 初始化失败"
		d.Hint = "常见于安全软件注入或运行库版本冲突；尝试加入白名单后重试"
	case statusDllNotFound:
		d.What = "缺少依赖 DLL"
		d.Hint = "检查安装目录是否被安全软件删掉文件，或重新安装"
	case statusEntryPointNotFound:
		d.What = "DLL 入口点缺失（运行库版本不匹配）"
		d.Hint = "确认安装目录里的 MinGW 运行库与主程序同批产出"
	case statusBadImageFormat:
		d.What = "映像格式错误（32/64 位 DLL 混用）"
		d.Hint = "安装目录里混入了位数不对的 DLL，清理后重装"
	case statusIllegalInstruction:
		d.What = "非法指令（CPU 不支持编译目标）"
		d.Hint = "确认目标机 CPU 支持该指令集，或改用更保守的编译参数"
	case statusStackOverflow:
		d.What = "栈溢出"
		d.Hint = "把日志发给交付方"
	case statusFailFast:
		d.What = "快速失败（运行时完整性检查触发）"
		d.Hint = "常见于安全软件注入或堆损坏；加入白名单后重试"
	default:
		d.What = "异常退出"
		d.Hint = "把日志发给交付方"
	}
	return d, true
}

// formatCode 只用于测试与日志的稳定输出，避免各处 fmt 写法漂移。
func formatCode(code uint32) string {
	return fmt.Sprintf("%d (0x%08X)", code, code)
}
