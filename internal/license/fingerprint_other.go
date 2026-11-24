//go:build !darwin

package license

// hardwareMACs 在非 macOS 平台上是空实现。
//
// Linux / Windows 上 net.Interfaces() 返回的就是真实网卡地址，不存在
// macOS 那种「当前地址被覆盖、出厂地址只留在配置层」的情况，因此直接
// 走通用的采样逻辑即可。返回 nil 会让 CollectLocalMACs 落到兜底分支。
func hardwareMACs() []string {
	return nil
}

// hardwareFingerprintNote 见 darwin 版本的同名常量。
const hardwareFingerprintNote = ""

// physicalPort 是 darwin 版本使用的端口描述，这里保留类型定义让上层
// 代码可以跨平台编译；非 macOS 平台永远返回空。
type physicalPort struct {
	Port   string
	Device string
	MAC    string
}

// physicalPorts 在非 macOS 平台上返回空：Linux / Windows 的
// net.Interfaces() 本身就是硬件地址，没有「当前地址 vs 出厂地址」
// 的区别，不需要额外分类。
func physicalPorts() (physical, logical []physicalPort) {
	return nil, nil
}

// isPhysicalPortName 在非 macOS 平台上恒为 true（不做名称过滤）。
func isPhysicalPortName(string) bool {
	return true
}

// MACInfo 见 darwin 版本的同名类型。
type MACInfo struct {
	Device string
	Port   string
	MAC    string
}

// PhysicalMACs 在非 macOS 平台上退回到通用采集结果。
//
// Linux / Windows 的 net.Interfaces() 拿到的就是硬件地址，没有逻辑端口
// 混入的问题，所以直接包装成 MACInfo 即可。
func PhysicalMACs() []MACInfo {
	macs := CollectLocalMACs()
	out := make([]MACInfo, 0, len(macs))
	for _, m := range macs {
		out = append(out, MACInfo{MAC: m})
	}
	return out
}

// PlatformNote 见 darwin 版本的同名函数。
func PlatformNote() string {
	return ""
}
