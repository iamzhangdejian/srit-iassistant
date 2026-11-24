//go:build darwin

package license

import (
	"context"
	"net"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// hardwareMACs 读取 macOS 设备的**出厂**网卡地址。
//
// 为什么需要单独一套逻辑：net.Interfaces() 返回的是「当前生效地址」。
// macOS 开启 Wi-Fi「私有地址」后，en0 的生效地址会被替换成一个随机
// 地址（本地管理位为 1），出厂的烧录地址在 BSD 层就看不到了。但系统
// 自己的配置层仍然记得它——`networksetup -getmacaddress en0` 报的是
// 硬件地址，而 `ifconfig en0` 报的是当前地址，两者可以不同。
//
// 实测对比（同一台 MacBook，Wi-Fi 私有地址开启）：
//
//	ifconfig en0        ether 86:af:b8:e4:0b:ab   ← 当前生效，随网络变化
//	networksetup        fc:b2:14:ba:44:f0         ← 硬件地址，不变
//
// 授权绑定显然要用后者：它不随重启、换网络、系统升级变化。
//
// 返回空切片表示读不到（例如系统缺少 networksetup，或在极端受限环境
// 下命令被拒绝执行）。调用方会退回到通用采集逻辑。
// MACInfo 是一个参与授权的物理网卡地址，附带人类可读的来源。
//
// 授权页要能回答「这个地址是哪块网卡的」——用户遇到授权失效时，第一件事
// 就是对比当前地址与授权码里记录的地址是否一致，只有地址见过没用。
type MACInfo struct {
	Device string // 接口名，如 en0
	Port   string // 硬件端口名，如 "Wi-Fi"
	MAC    string // 出厂地址
}

// PhysicalMACs 只返回物理网卡的出厂地址，按「Wi-Fi / 有线优先」排序。
//
// 与 CollectLocalMACs 的区别：后者是「能用来绑定授权的一组地址」，宁可
// 多收也不漏；这个函数是「授权页展示的物理网卡清单」，会把虚拟桥、VLAN、
// 链路聚合这些逻辑端口排除掉。
//
// 两者不冲突：安全判断始终走 CollectLocalMACs + Matches（交集非空）。
// 这里少列几个地址，只是少一条可用于匹配的记录，不会让本来能通过的
// 机器变成通不过——因为 CollectLocalMACs 已经把物理地址包含在内了。
func PhysicalMACs() []MACInfo {
	physical, _ := physicalPorts()
	if len(physical) == 0 {
		return nil
	}
	out := make([]MACInfo, 0, len(physical))
	for _, p := range physical {
		out = append(out, MACInfo{Device: p.Device, Port: p.Port, MAC: p.MAC})
	}
	return out
}

func hardwareMACs() []string {
	physical, _ := physicalPorts()

	// 兜底校验：networksetup 有时会把已拔掉的端口也列出来，确认接口
	// 此刻确实存在。这里不按标志位过滤——接口名对了就够了。
	live := make(map[string]struct{})
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			live[iface.Name] = struct{}{}
		}
	}

	macs := make([]string, 0, len(physical))
	for _, p := range physical {
		if len(live) > 0 {
			if _, ok := live[p.Device]; !ok {
				continue
			}
		}
		macs = append(macs, p.MAC)
	}
	if len(macs) == 0 {
		return nil
	}
	return macs
}

// physicalPorts 把 networksetup 列出的硬件端口分成「真物理网卡」与
// 「逻辑端口 / 虚拟桥」两类。
//
// 判据——同时满足才算物理网卡：
//   - 端口名（Hardware Port）不含 Bridge / VLAN / Aggregate / Bond；
//   - 出厂地址有效，且本地管理位为 0（厂商烧录）。
//
// 为什么还要判本地管理位：Thunderbolt Bridge 会报
// 02:00:00:00:00:00，那本来就是系统生成的地址，不是硬件地址。
//
// 这个分类只用于「授权页显示哪些地址」，不用于安全判断。绑定校验走的
// 仍是 Matches 的交集非空，多一个少一个地址不改变安全边界。
func physicalPorts() (physical, logical []physicalPort) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	path, err := exec.LookPath("networksetup")
	if err != nil {
		return nil, nil
	}
	out, err := exec.CommandContext(ctx, path, "-listallhardwareports").Output()
	if err != nil {
		return nil, nil
	}

	var port, device, ether string
	flush := func() {
		if device == "" || ether == "" {
			port, device, ether = "", "", ""
			return
		}
		entry := physicalPort{Port: port, Device: device, MAC: ether}
		if isPhysicalPortName(port) && IsUsableMAC(ether) && isHardwareMAC(ether) {
			physical = append(physical, entry)
		} else {
			logical = append(logical, entry)
		}
		port, device, ether = "", "", ""
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Hardware Port:"):
			flush()
			port = strings.TrimSpace(strings.TrimPrefix(line, "Hardware Port:"))
		case strings.HasPrefix(line, "Device:"):
			device = strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
		case strings.HasPrefix(line, "Ethernet Address:"):
			ether = strings.TrimSpace(strings.TrimPrefix(line, "Ethernet Address:"))
		}
	}
	flush()

	sort.SliceStable(physical, func(i, j int) bool {
		pi, pj := primaryRank(physical[i].Device), primaryRank(physical[j].Device)
		if pi != pj {
			return pi < pj
		}
		return physical[i].Device < physical[j].Device
	})
	return physical, logical
}

// physicalPort 是 networksetup 报出的一个硬件端口。
type physicalPort struct {
	Port   string // Hardware Port，如 "Wi-Fi"、"Thunderbolt 1"
	Device string // 接口名，如 "en0"
	MAC    string // 出厂地址
}

// isPhysicalPortName 按端口名排除逻辑端口。
//
// networksetup 把桥接、VLAN、链路聚合也算作「Hardware Port」，但它们的
// 地址是系统合成的，不是网卡烧录的。名字是最稳的判据——比地址的第一
// 字节更直观，也不会因为某个厂商用光了前缀而误判。
func isPhysicalPortName(port string) bool {
	lower := strings.ToLower(port)
	for _, bad := range []string{"bridge", "vlan", "aggregate", "bond", "bonded", "link aggregate"} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	return true
}

// hardwareFingerprintNote 供授权页展示「当前地址来自硬件还是系统当前值」。
//
// 保留为一个常量而不是运行时判断：判断逻辑已经在 hardwareMACs 里，
// 这里只提供文案，避免前端再猜一次。
const hardwareFingerprintNote = "macOS：优先使用网卡出厂地址（networksetup）"

// PlatformNote 返回「本机地址是怎么取到的」，供授权页与日志展示。
func PlatformNote() string {
	return hardwareFingerprintNote
}
