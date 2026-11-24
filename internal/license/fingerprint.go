package license

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FingerprintLen 是机器指纹的十六进制字符数。
//
// 20 个字符 = 80 位，碰撞概率远低于同型号机器的保有量，同时比完整的
// 64 位 SHA256 短得多——指纹要显示在授权页上让用户念给客服听，
// 太长会显著增加沟通成本。
const FingerprintLen = 20

// Fingerprint 是「这台机器」的稳定标识：由所有物理网卡的 MAC 地址
// 排序后聚合而成。
//
// 为什么不是单一 MAC：见包注释第 3 条。这里补一个实现层面的原因——
// 用户插拔网线、开关 Wi-Fi 会改变**网卡列表**，但不会改变已存在网卡的
// MAC 本身。所以正确做法是把授权码里保存的 MAC 集合与当前机器的 MAC
// 集合做**交集**判断（见 Matches），而不是要求两边完全相等。
//
// 空列表返回空串，调用方应当把空指纹视为「无法绑定」而不是「绑定成功」。
func Fingerprint(macs []string) string {
	normalized := NormalizeMACs(macs)
	if len(normalized) == 0 {
		return ""
	}
	h := sha256.New()
	// 用 '\n' 分隔而不是连接后直接哈希：否则 ["aabb","cc"] 与
	// ["aabc","c"] 会得到同一个结果。
	for _, m := range normalized {
		h.Write([]byte(m))
		h.Write([]byte{'\n'})
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > FingerprintLen {
		sum = sum[:FingerprintLen]
	}
	return strings.ToUpper(sum)
}

// NormalizeMACs 把任意写法的 MAC 统一成「小写、冒号分隔、去重、排序」。
//
// 排序是为了让指纹与输入顺序无关：发码端用户粘贴的 MAC 顺序，与客户端
// 枚举网卡的顺序必然不同，不排序就等于每次都对不上。
func NormalizeMACs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		mac, ok := normalizeMAC(item)
		if !ok {
			continue
		}
		if _, dup := seen[mac]; dup {
			continue
		}
		seen[mac] = struct{}{}
		out = append(out, mac)
	}
	sort.Strings(out)
	return out
}

// normalizeMAC 解析单个 MAC 地址，兼容三种常见写法：
//
//	AA:BB:CC:DD:EE:FF   冒号分隔（macOS/Linux 常见）
//	AA-BB-CC-DD-EE-FF   连字符分隔（Windows 常见）
//	AABBCCDDEEFF        12 位裸十六进制（部分设备标签）
//
// 同时接受大小写混写与首尾空白。非法输入返回 ok=false 而不是错误：
// 批量导入时我们希望跳过坏行、继续处理其余行。
func normalizeMAC(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", ":")
	s = strings.ReplaceAll(s, ".", ":")
	// 裸十二位十六进制：每两位插一个冒号。
	if len(s) == 12 && !strings.Contains(s, ":") {
		var b strings.Builder
		b.Grow(17)
		for i := 0; i < 12; i += 2 {
			if i > 0 {
				b.WriteByte(':')
			}
			b.WriteString(s[i : i+2])
		}
		s = b.String()
	}
	hw, err := net.ParseMAC(s)
	if err != nil {
		return "", false
	}
	// net.ParseMAC 认识 EUI-64 等更长的形式，这里只接受标准 6 字节 MAC。
	if len(hw) != 6 {
		return "", false
	}
	return hw.String(), true
}

// IsUsableMAC 判断一个 MAC 是否可以用来绑定授权。
//
// 只排除四类**确定无效**的地址：长度不是 6 字节或无法解析、全零
// （网卡还没有分配地址）、广播 ff:ff:ff:ff:ff:ff、组播（第一字节
// 最低位为 1，如 01:00:5e:... 与 macOS 的 awdl0 / llw0 / nan0）。
//
// 这里**不**判断「本地管理位（第一字节次低位，0x02）」。位为 1 说明
// 这个地址是软件指定的，不是厂商烧录的——理论上更不稳定，但它不是
// 「不可用」。把这一位当作硬性过滤条件，在真机上的后果是灾难性的：
// macOS 的 Wi-Fi「私有地址」默认开启，企业 MDM、VPN 客户端、
// Clash/Surge 这类工具也会给物理网卡改写地址，实测一台正常 MacBook
// 上 17 块网卡的首字节次低位**全部**为 1。按此过滤等于宣告「这台
// 机器没有网卡」，授权流程直接走不通。
//
// 「只要物理地址」这个诉求由 hardwareMACs 在更上层解决——它优先读
// 设备真实的出厂地址，见该函数注释。
func IsUsableMAC(mac string) bool {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil || len(hw) != 6 {
		return false
	}
	if hw[0]|hw[1]|hw[2]|hw[3]|hw[4]|hw[5] == 0 { // 全零
		return false
	}
	if hw[0]&hw[1]&hw[2]&hw[3]&hw[4]&hw[5] == 0xff { // 广播
		return false
	}
	if hw[0]&0x01 != 0 { // 组播，含 awdl0 / llw0 / nan0
		return false
	}
	return true
}

// isHardwareMAC 判断一个地址是否来自厂商烧录（本地管理位为 0）。
//
// 与 IsUsableMAC 的分工：IsUsableMAC 回答「能不能用来绑定」，
// isHardwareMAC 回答「它是不是物理地址」。前者是安全底线，后者是
// 采集时的优先级。
func isHardwareMAC(mac string) bool {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil || len(hw) != 6 {
		return false
	}
	return hw[0]&0x02 == 0
}

// hardwareInterfaceNames 返回本机上「物理网卡」的接口名集合。
//
// 用途：在 macOS 上给通用采集逻辑做交叉校验。networksetup 的硬件端口
// 列表里会出现 Thunderbolt Bridge 这类逻辑端口，它们的地址是
// 02:00:00:00:00:00 这种无效值，靠 IsUsableMAC 就能滤掉；只剩真正的
// 物理端口（Wi-Fi、Thunderbolt、USB 网卡）能读到出厂地址。
//
// 命令不可用或执行失败时返回空集合，调用方会退回到「不过滤接口名」。
func hardwareInterfaceNames() map[string]struct{} {
	names := make(map[string]struct{})
	path, err := exec.LookPath("networksetup")
	if err != nil {
		return names
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-listallhardwareports").Output()
	if err != nil {
		return names
	}
	var pending string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Device:") {
			pending = strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			continue
		}
		if strings.HasPrefix(line, "Ethernet Address:") && pending != "" {
			addr := strings.TrimSpace(strings.TrimPrefix(line, "Ethernet Address:"))
			if IsUsableMAC(addr) && isHardwareMAC(addr) {
				names[pending] = struct{}{}
			}
			pending = ""
		}
	}
	return names
}

// CollectLocalMACs 枚举本机可用的网卡 MAC。
//
// 分两层：
//
//  1. hardwareMACs()：读设备真实的出厂地址。macOS 上 net.Interfaces()
//     返回的是「当前生效地址」，被 Wi-Fi 私有地址或网络工具覆盖后就
//     看不到出厂值了；但 IOKit 里仍保留着。networksetup 能读出来。
//  2. 兜底：遍历 net.Interfaces()，用 IsUsableMAC 过滤明显无效的地址。
//
// 只有在第一层「一个物理地址都没读到」时才用第二层。这样绝大多数
// 机器拿到的就是稳定的出厂地址，重启、换网络都不会变；而在那些真的
// 读不到出厂地址的环境里（虚拟机、受限容器、Linux 上的随机化），
// 至少还有一个地址可用，不至于卡死在授权页。
//
// 返回顺序：en0 在前，en1..en9 次之，其余按接口名。授权页只展示前
// 几个，用户复制到的更可能是 Wi-Fi 或有线的地址。
//
// 不区分 Wi-Fi 与有线，两者都收：一台笔记本两种都可能有，全收进来
// 提高了「至少命中一个」的概率，换网络环境时更不容易掉授权。
func CollectLocalMACs() []string {
	// 运维逃生舱：某些机器（云桌面、受限容器、全虚拟网卡环境）读不到
	// 任何一个可用的网卡 MAC，此时按「拒绝」策略用户会被彻底挡在门外。
	// 允许运维通过环境变量显式指定一个稳定锚点（比如宿主机 MAC、资产
	// 台账里登记的 MAC），代价是要人工保证它在这台机器上不变。
	override := normalizeMACOverride(os.Getenv("WK_LICENSE_MAC_OVERRIDE"))

	// 第一层：物理网卡的出厂地址。绝大多数机器在这里就结束了，
	// 拿到的是重启、换网络都不变的稳定值。
	if hw := hardwareMACs(); len(hw) > 0 {
		return NormalizeMACs(append(hw, override...))
	}

	// 第二层：兜底。用 net.Interfaces() 的当前生效地址，只保留通过
	// IsUsableMAC 的项。这一段在 macOS 上很少走到（除非 networksetup
	// 不可用），主要给 Linux / Windows 与受限环境用。
	ifaces, err := net.Interfaces()
	if err != nil {
		return override
	}

	type candidate struct {
		name string
		addr string
	}
	found := make([]candidate, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}
		addr := iface.HardwareAddr.String()
		// IsUsableMAC 挡掉全零 / 广播 / 组播：Thunderbolt Bridge 报的
		// 02:00:00:00:00:00 在这一步被滤掉。软件地址仍可能留下（比如
		// Wi-Fi 私有地址），所以下面排序时物理地址排前面。
		if !IsUsableMAC(addr) {
			continue
		}
		found = append(found, candidate{name: iface.Name, addr: addr})
	}

	if len(found) == 0 {
		return override
	}

	// 排序：先按「是否物理地址」，再按 en0/en1 顺序。
	sort.SliceStable(found, func(i, j int) bool {
		hi, hj := isHardwareMAC(found[i].addr), isHardwareMAC(found[j].addr)
		if hi != hj {
			return hi // 物理地址排前面
		}
		pi, pj := primaryRank(found[i].name), primaryRank(found[j].name)
		if pi != pj {
			return pi < pj
		}
		return found[i].name < found[j].name
	})

	raw := make([]string, 0, len(found))
	for _, c := range found {
		raw = append(raw, c.addr)
	}
	return NormalizeMACs(append(raw, override...))
}

// primaryRank 给接口名打分：en0 排最前，en1..en9 次之，其余最后。
//
// 只是排序提示，不影响可用性判断——排后面的地址一样会参与匹配。
func primaryRank(name string) int {
	if name == "en0" {
		return 0
	}
	if strings.HasPrefix(name, "en") {
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "en")); err == nil && n < 10 {
			return 1 + n
		}
		return 20
	}
	return 100
}

// normalizeMACOverride 解析运维显式指定的锚点 MAC。
//
// 只在采不到任何物理网卡时才会被调用，见 CollectLocalMACs 的注释。
// 解析失败返回 nil，即「仍然没有可用网卡」，比返回一个错的值安全。
func normalizeMACOverride(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	}) {
		if !IsUsableMAC(part) {
			continue
		}
		out = append(out, part)
	}
	return NormalizeMACs(out)
}

// Matches 判断一张授权码绑定的 MAC 集合是否与当前机器匹配。
//
// 规则是**交集非空**，不是完全相等。原因：
//
//   - 用户插了网线、开了热点、连了 VPN，网卡数量会变，但原有网卡
//     的 MAC 不变。要求完全相等会让「插网线」这种日常操作导致掉授权。
//   - 反过来，只要有一块网卡匹配，就足以说明是同一台物理机器——
//     伪造一块特定 MAC 的网卡当然可行，但那是硬件级攻击，成本远高于
//     「买第二份授权」。
//
// 授权码没有记录任何 MAC（老码或异常码）时返回 false，交由调用方把
// 「码本身不完整」与「机器不对」区分开提示。
func Matches(licenseMACs, localMACs []string) bool {
	want := NormalizeMACs(licenseMACs)
	have := NormalizeMACs(localMACs)
	if len(want) == 0 || len(have) == 0 {
		return false
	}
	haveSet := make(map[string]struct{}, len(have))
	for _, m := range have {
		haveSet[m] = struct{}{}
	}
	for _, m := range want {
		if _, ok := haveSet[m]; ok {
			return true
		}
	}
	return false
}

// MatchesFingerprint 判断授权码里的指纹是否与「当前机器的 MAC 集合」
// 一致。
//
// 先算当前机器指纹再比较，与 Matches 的差别在于它认的是完整指纹。
// 两个函数都保留：Matches 用于「用户可能只迁移了部分网卡」的宽松场景，
// MatchesFingerprint 用于需要严格一致的自检场景（如发码端的验签自检）。
func MatchesFingerprint(wantFP string, localMACs []string) bool {
	wantFP = strings.TrimSpace(wantFP)
	if wantFP == "" {
		return false
	}
	return strings.EqualFold(wantFP, Fingerprint(localMACs))
}
