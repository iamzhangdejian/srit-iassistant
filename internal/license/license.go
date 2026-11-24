// Package license 实现「授权码」的解析与校验，供桌面端在登录与请求
// 阶段判定当前机器是否被授权、能用到什么时候、能用哪些功能。
//
// 设计要点（每一条都有它要解决的问题，改动前请先读）：
//
//  1. 授权码是「载荷 + 签名」两段式，不是裸字符串。
//     格式：WNL1.<base64url(payload_json)>.<base64url(ed25519_sig)>
//     裸字符串无法离线验证真伪；分成三段后，只要客户端的公钥还在，
//     用户就伪造不出新码。
//
//  2. 签名用 Ed25519（非对称），不用 HMAC。
//     HMAC 的密钥必须随客户端分发，逆向一次就能无限发码，等于没有授权。
//     Ed25519 只把公钥放客户端，私钥永远留在发码服务上。
//
//  3. 绑定的是「机器指纹」，而不是某一个 MAC 地址。
//     一台笔记本同时有 Wi-Fi、有线、蓝牙几个网卡，且 macOS 的「私有
//     Wi-Fi 地址」会让 Wi-Fi 的 MAC 换网络就变。只认一个 MAC 会导致
//     用户换个咖啡馆就登不进去——这是真实的投诉来源，不是理论风险。
//     因此指纹 = 全部物理网卡 MAC 排序后聚合的哈希；授权码里同时保留
//     原始 MAC 列表，方便发码端人工核对「是不是这台机器」。
//
//  4. 本包只做纯函数式的解析与判定，不碰磁盘、不碰时钟。
//     调用方传入 now 和已保存的状态，方便测试覆盖「时间回拨」「旧码
//     回滚」这些边界；持久化在 store.go。
package license

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CodePrefix 是授权码的版本前缀。
//
// 有了它，格式校验时第一眼就能判断「这串东西是不是授权码、是不是本版本
// 能解析的格式」，不必先做 base64 解码再猜。将来换签名算法或改载荷结构
// 时，用 WNL2. 与旧码区分，老客户端会明确报「版本不支持」而不是「签名
// 无效」，对客服排查很重要。
const CodePrefix = "WNL1"

// 解析与校验过程中可能出现的错误。调用方据此决定给用户什么提示。
var (
	// ErrEmptyCode 表示没有提供授权码。
	ErrEmptyCode = errors.New("license: empty code")
	// ErrBadFormat 表示三段式结构不成立（前缀缺失、段数不对）。
	ErrBadFormat = errors.New("license: malformed code")
	// ErrBadEncoding 表示某一段不是合法的 base64url。
	ErrBadEncoding = errors.New("license: invalid encoding")
	// ErrBadPayload 表示载荷不是合法 JSON 或字段缺失。
	ErrBadPayload = errors.New("license: invalid payload")
	// ErrBadSignature 表示签名验证不通过。
	//
	// 注意：对外提示时**不要**区分「签名长度不对」和「签名内容不符」，
	// 否则等于告诉攻击者他离成功有多近。本包内部区分错误细节，
	// 提示文案统一成「授权码无效或已被篡改」。
	ErrBadSignature = errors.New("license: signature verification failed")
	// ErrFingerprintMismatch 表示授权码不是签发给当前这台机器的。
	ErrFingerprintMismatch = errors.New("license: machine fingerprint mismatch")
	// ErrExpired 表示授权已过期。
	ErrExpired = errors.New("license: expired")
	// ErrRollback 表示试图用一个更早到期的旧授权码覆盖当前的。
	ErrRollback = errors.New("license: license code is older than the one already applied")
)

// Feature 是功能位图的取值。
//
// 第一版只做四档，够用且好向客户解释。刻意不做「知识库数量上限」这类
// 细粒度数值限制：数值限制一旦写进授权码，客户提一句需求就要重发码，
// 而重发码的成本远高于收益。
type Feature string

const (
	// FeatureKB 知识库的创建、导入、解析与检索。
	FeatureKB Feature = "kb"
	// FeatureChat 对话问答（含联网搜索开关）。
	FeatureChat Feature = "chat"
	// FeatureOCR 图片与扫描件的 OCR 识别。
	FeatureOCR Feature = "ocr"
	// FeatureOffice 通过技能生成 PPT / Word / Excel 产物。
	FeatureOffice Feature = "office"
)

// AllFeatures 是四档功能的全集，用于「全部授权」与合法性校验。
//
// 顺序固定，便于在界面上稳定地展示勾选项，也便于测试做集合比较。
var AllFeatures = []Feature{FeatureKB, FeatureChat, FeatureOCR, FeatureOffice}

// IsKnownFeature 判断某个功能标识是否在本版本认识的四档之内。
func IsKnownFeature(f Feature) bool {
	for _, known := range AllFeatures {
		if known == f {
			return true
		}
	}
	return false
}

// Limits 是随授权码一起下发的数量上限。
//
// 与「功能位图」分开是因为两者的语义不同：位图决定「能不能用」，
// 上限决定「最多用多少」。第一版只保留两个最常被问起的上限，
// 缺省为 0 表示「不限制」，避免新增字段时老码解析失败。
type Limits struct {
	// KB 是最多可创建的知识库数量，0 表示不限。
	KB int `json:"kb,omitempty"`
	// Seat 是允许同时在线的用户数，0 表示不限。桌面端单用户场景恒为 1。
	Seat int `json:"seat,omitempty"`
}

// Payload 是授权码里被签名的部分。
//
// 字段一律用短的 JSON 名：授权码是要人手工复制粘贴的，载荷越短越好复制
// 越不容易断行。同时保留 iat/exp 用 Unix 秒，避免时区与格式解析的坑。
type Payload struct {
	// V 是载荷结构版本。目前恒为 1，与 CodePrefix 的版本号独立：
	// 万一将来只在载荷里加字段而不换签名算法，可以只动这里。
	V int `json:"v"`
	// SN 是授权序列号，发码端保证唯一。用于「按使用人查旧码」「作废旧码」。
	SN string `json:"sn"`
	// FP 是机器指纹，见 Fingerprint。
	FP string `json:"fp"`
	// MAC 是签发时绑定的原始 MAC 地址列表（小写、冒号分隔、已排序）。
	//
	// 它**不参与**校验（校验只认 FP），存在的唯一目的是让人能看懂
	// 「这张码是绑给哪几块网卡的」——客服核对与用户自查都靠它。
	MAC []string `json:"mac"`
	// IAT 是签发时间（Unix 秒）。
	IAT int64 `json:"iat"`
	// EXP 是到期时间（Unix 秒）。0 表示永久授权。
	EXP int64 `json:"exp"`
	// Feat 是功能位图。
	Feat []Feature `json:"feat"`
	// Lim 是数量上限，可为空。
	Lim Limits `json:"lim"`
	// Org 是使用单位，便于台账检索。
	Org string `json:"org,omitempty"`
	// User 是使用人，换机重签时靠它把新旧码串起来。
	User string `json:"user,omitempty"`
	// KID 是签发者公钥指纹（KeyID）。
	//
	// 它的唯一用途是**排障**：验签失败时，把「这张码是谁签的」与
	// 「本机只认谁」摆在一起，用户和运维立刻能看出是换过密钥，
	// 而不是去怀疑码被篡改或复制不完整。
	// 老码没有这个字段，空值表示「签发者未知」，此时只能按验签结果判定。
	KID string `json:"kid,omitempty"`
	// RST 表示这是一张「复位码」：导入时丢弃历史到期水位。
	//
	// 它解决的是死锁：某个更早到期的合法码是唯一能救回用户的码，
	// 却被回滚保护挡住。复位码把「清水位」的权力留在发码端——
	// 想复位就必须持私钥签发，客户端不向用户开放任何自助绕过入口。
	// 复位本身不放大权限：功能位、机器绑定、到期时间照样生效。
	RST bool `json:"rst,omitempty"`
}

// KeyID 返回公钥指纹，用于把「签发者」与「本机信任的公钥」对上号。
//
// 取 SHA-256 的前 16 个十六进制字符：既短到能显示在界面上，又足够
// 避免两把不同密钥撞车。它不是密码学意义上的标识符，只服务于排障。
func KeyID(publicKey []byte) string {
	if len(publicKey) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:8])
}

// Code 是一张已通过签名验证的授权码。
type Code struct {
	// Payload 是签名覆盖的原始载荷。
	Payload Payload
	// Raw 是调用方传进来的原始授权码文本（已去除首尾空白），
	// 便于把它原样存盘、原样回显给用户复制。
	Raw string
}

// Encode 把载荷用私钥签名，产出可分发的授权码文本。
//
// 这个函数只在发码端（cmd/licadmin）使用。把它放在同一个包里，是为了
// 让「签名格式」与「验签格式」共享一份实现——两处各写一遍迟早会漂移。
func Encode(payload Payload, privateKey ed25519.PrivateKey) (string, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("license: private key must be %d bytes, got %d",
			ed25519.PrivateKeySize, len(privateKey))
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("license: encode payload: %w", err)
	}
	sig := ed25519.Sign(privateKey, body)
	return CodePrefix + "." +
		base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// Decode 解析授权码并验证签名。
//
// 它**不**判断到期、不判断机器指纹——那两件事依赖「当前时间」与「当前
// 机器」，属于判定而不是解析。分开的好处是发码端的「验签自检」能复用
// 这个函数，而不必假装自己是某台机器。
func Decode(raw string, publicKey ed25519.PublicKey) (*Code, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrEmptyCode
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: public key must be %d bytes, got %d",
			ed25519.PublicKeySize, len(publicKey))
	}

	// 用户从邮件或聊天窗口复制时，长串码常被自动换行，必须容忍中间的
	// 空白与换行，否则「复制过来就报格式错」会变成主要投诉来源。
	cleaned := stripSpace(raw)
	parts := strings.Split(cleaned, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected 3 segments, got %d", ErrBadFormat, len(parts))
	}
	if parts[0] != CodePrefix {
		return nil, fmt.Errorf("%w: unsupported prefix %q", ErrBadFormat, parts[0])
	}

	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// 兼容带 padding 的写法：有些导出工具会补 '='，
		// 虽然不合规范但没必要因此拒绝一张合法授权。
		body, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("%w: payload is not base64url", ErrBadEncoding)
		}
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		sig, err = base64.URLEncoding.DecodeString(parts[2])
		if err != nil {
			return nil, fmt.Errorf("%w: signature is not base64url", ErrBadEncoding)
		}
	}

	// 先验签再解析 JSON。顺序很重要：未经签名保护的 JSON 可以塞入任意
	// 结构，先解析等于让攻击者决定我们解析什么。验签通过后的字节才可信。
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: signature length %d", ErrBadSignature, len(sig))
	}
	if !ed25519.Verify(publicKey, body, sig) {
		return nil, ErrBadSignature
	}

	var payload Payload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	if err := payload.Validate(); err != nil {
		return nil, err
	}

	return &Code{Payload: payload, Raw: cleaned}, nil
}

// Validate 检查载荷本身是否自洽。
//
// 它只做「结构上说不通」的判定，不涉及时间与机器。签名覆盖了这些字段，
// 但签名有效不代表内容合理——发码端的程序缺陷完全可能签出一张没有
// 指纹的码，那种码必须在使用前就被拦住，而不是让用户在登录时看到一个
// 莫名其妙的「机器不匹配」。
func (p Payload) Validate() error {
	if p.V != 1 {
		return fmt.Errorf("%w: unsupported payload version %d", ErrBadPayload, p.V)
	}
	if strings.TrimSpace(p.SN) == "" {
		return fmt.Errorf("%w: missing serial number", ErrBadPayload)
	}
	if strings.TrimSpace(p.FP) == "" {
		return fmt.Errorf("%w: missing machine fingerprint", ErrBadPayload)
	}
	if p.IAT <= 0 {
		return fmt.Errorf("%w: missing issued-at time", ErrBadPayload)
	}
	if p.EXP < 0 {
		return fmt.Errorf("%w: negative expiry", ErrBadPayload)
	}
	// 到期时间早于签发时间说明这张码一发出就已作废，几乎必然是发码时
	// 填错了日期（比如把「有效期天数」填成了绝对时间）。
	if p.EXP != 0 && p.EXP <= p.IAT {
		return fmt.Errorf("%w: expiry is not after issued-at", ErrBadPayload)
	}
	for _, f := range p.Feat {
		if !IsKnownFeature(f) {
			return fmt.Errorf("%w: unknown feature %q", ErrBadPayload, f)
		}
	}
	return nil
}

// HasFeature 判断这张码是否授权了某个功能。
func (p Payload) HasFeature(f Feature) bool {
	for _, granted := range p.Feat {
		if granted == f {
			return true
		}
	}
	return false
}

// ExpiresAt 返回到期时间；0 表示永久有效。
func (p Payload) ExpiresAt() int64 { return p.EXP }

// IsPermanent 表示这是一张不过期的授权（EXP == 0）。
func (p Payload) IsPermanent() bool { return p.EXP == 0 }

// stripSpace 去掉字符串里所有空白字符。
//
// 用 rune 遍历而不是正则，是因为授权码本身是 ASCII，但复制过程可能带入
// 全角空格或不换行空格（U+00A0），strings.ReplaceAll 处理不了后者。
func stripSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\t', '\r', '\n', '\u00A0', '\u3000':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ParsePublicKey 解析 base64 编码的 Ed25519 公钥。
//
// 接受标准 base64 与 URL 变体、带不带 padding 都行——公钥经常是从
// 发码端页面复制出来的，用户在中间加了空格或换行是常事，这里一并
// 容错，避免「粘贴没错但就是验不过」这种极难自查的问题。
func ParsePublicKey(raw string) ([]byte, error) {
	cleaned := stripSpace(raw)
	if cleaned == "" {
		return nil, ErrBadEncoding
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(cleaned); err == nil {
			if len(decoded) != ed25519.PublicKeySize {
				return nil, ErrBadEncoding
			}
			return decoded, nil
		}
	}
	return nil, ErrBadEncoding
}
