package license

import (
	"fmt"
	"time"
)

// State 描述当前授权处于什么状态。它是调用方做取舍的唯一依据：
// 中间件据此决定放行还是拒绝，前端据此决定显示哪个页面。
type State string

const (
	// StateValid 表示授权有效，全部已授权功能可用。
	StateValid State = "valid"
	// StateGrace 表示已过期但仍在宽限期内。
	//
	// 宽限期存在的理由很实际：用户可能正在写一份报告，某个下午授权到
	// 期了，如果当场锁死，他丢的是工作成果而不是钱。降级到只读 +
	// 持续提醒，既不影响业务，又足以驱动续费。
	StateGrace State = "grace"
	// StateReadOnly 表示已过期且超过宽限期：只能查看已有内容，不能新增。
	StateReadOnly State = "readonly"
	// StateMissing 表示从未导入过授权码。
	StateMissing State = "missing"
	// StateInvalid 表示授权码本身有问题（签名错、机器不符、已作废）。
	StateInvalid State = "invalid"
)

// GracePeriod 是过期后仍可只读使用的天数。
const GracePeriod = 7 * 24 * time.Hour

// ClockSkewTolerance 允许的时钟回拨误差。
//
// 用户改系统时间几乎必然是为了绕过到期，但时区切换、NTP 校时、休眠
// 唤醒也会让系统时间短暂倒退。留 1 天的容差，把「正常的时钟抖动」与
// 「明显在往回拨」区分开。
const ClockSkewTolerance = 24 * time.Hour

// Record 是本地保存的「当前授权」及其历史观测值。
//
// 它既是授权码本身，也是防回滚与防时间回拨的依据，所以必须整体保存、
// 整体校验，不能只存授权码字符串。
type Record struct {
	// Code 是原始授权码文本。
	Code string `json:"code"`
	// AppliedAt 是这张码被导入的时间（Unix 秒）。
	AppliedAt int64 `json:"applied_at"`
	// MaxExpSeen 是「历史上见过的最晚到期时间」（Unix 秒）。
	//
	// 有它才能拦住回滚攻击：用户拿一张早就到期的旧码覆盖新码，如果只看
	// 新导入的码本身，它确实是一张签名有效的合法码，无法分辨新旧。
	// 与历史最大值比较，旧码必然更小，于是被拒。
	MaxExpSeen int64 `json:"max_exp_seen"`
	// LastSeenAt 是最近一次启动/校验时观测到的系统时间（Unix 秒）。
	//
	// 与当前时间比较可以检测「把系统时钟调回去续命」。取历史最大观测值
	// 而不是覆盖写，是为了防止攻击者反复小步回拨绕开单次比对。
	LastSeenAt int64 `json:"last_seen_at"`
	// KeyID 是保存这张码时的签发者公钥指纹。
	//
	// 与 Payload.KID 分开保存的原因：Payload 里的 KID 来自码本身（老码
	// 没有），而这里记录的是「本机当时信任的那把公钥」——换密钥之后
	// 两者不一致，正是需要向运维说清楚的事。老记录没有这个字段，
	// 空值表示未知，此时以「码能否验签」作为唯一判据。
	KeyID string `json:"key_id,omitempty"`
}

// Evaluation 是一次完整判定的结果。
type Evaluation struct {
	// State 是判定出的状态。
	State State `json:"state"`
	// Payload 是已验签的载荷；State 为 missing/invalid 时为 nil。
	Payload *Payload `json:"payload,omitempty"`
	// ExpiresAt 是到期时间（Unix 秒），0 表示永久。
	ExpiresAt int64 `json:"expires_at"`
	// RemainingDays 是剩余天数。已过期时按向下取整给出负数或 0。
	RemainingDays int `json:"remaining_days"`
	// Features 是**当前实际可用**的功能集合。
	//
	// 宽限期与只读期不返回完整位图，而是返回一个受限集合——调用方
	// 不必自己判断「这个状态下聊天还能不能用」，避免各处重复实现。
	Features []Feature `json:"features"`
	// Reason 是给人看的原因，前端直接显示。
	Reason string `json:"reason,omitempty"`
	// CurrentFingerprint 是当前机器的指纹。
	//
	// 即使授权无效也要给出来：授权页需要把它显示给用户，让他复制后
	// 发给发码方重签。这是「换机」流程的起点。
	CurrentFingerprint string `json:"current_fingerprint"`
	// BoundMACs 是授权码里记录的 MAC 列表，用于界面上对比「绑的是谁」。
	BoundMACs []string `json:"bound_macs,omitempty"`
}

// readOnlyFeatures 是只读状态下仍然可用的功能。
//
// 保留 kb 与 chat：用户必须还能打开并检索自己已有的知识库、还能提问，
// 否则「降级」就变成了「锁死」，与产品承诺不符。ocr 与 office 属于
// 「产生新内容」的能力，在只读期关闭。
var readOnlyFeatures = []Feature{FeatureKB, FeatureChat}

// Evaluate 根据已保存的记录与当前机器，判定授权状态。
//
//	record   本地保存的记录，nil 或 Code 为空表示从未导入
//	publicKey 内置公钥
//	localMACs 当前机器所有网卡的 MAC
//	now      当前时间（由调用方传入，便于测试）
//
// 判定顺序是刻意安排的：先看「有没有码」，再看「码真不真」，然后
// 「是不是这台机器」，最后才是时间。这样错误提示能一路指向真正的问题，
// 而不会出现「明明机器不对却报已过期」这种让人抓狂的结论。
func Evaluate(record *Record, publicKey []byte, localMACs []string, now time.Time) Evaluation {
	fp := Fingerprint(localMACs)
	base := Evaluation{
		CurrentFingerprint: fp,
		Features:           nil,
	}

	if record == nil || trimSpace(record.Code) == "" {
		base.State = StateMissing
		base.Reason = "尚未导入授权码"
		return base
	}

	code, err := Decode(record.Code, publicKey)
	if err != nil {
		base.State = StateInvalid
		base.Reason = describeDecodeError(err)
		return base
	}
	payload := code.Payload
	base.Payload = &payload
	base.ExpiresAt = payload.EXP
	base.BoundMACs = payload.MAC

	// 先单独处理「本机没有可用网卡」：这与「网卡不匹配」的处置方式
	// 完全不同——前者用户改不了（虚拟化环境、受限容器），得找运维；
	// 后者用户能自己核对。混成一条文案会让用户徒劳地去排查网卡。
	if len(localMACs) == 0 {
		base.State = StateInvalid
		base.Reason = "本机没有可用于授权的物理网卡，" +
			"请联系运维人员为这台机器单独签发授权码"
		return base
	}

	// 机器校验用「交集非空」而不是指纹相等：一台机器可能同时有 Wi-Fi
	// 与有线网卡，客户换网卡的组合方式很难预先猜全（见 Matches 注释）。
	// 指纹只作为兜底——当授权码里没记 MAC 列表（理论上不可能，但发码端
	// 的缺陷不该让用户买单）时，退回用指纹比对。
	machineOK := false
	if len(payload.MAC) > 0 {
		machineOK = Matches(payload.MAC, localMACs)
	} else {
		machineOK = MatchesFingerprint(payload.FP, localMACs)
	}
	if !machineOK {
		base.State = StateInvalid
		base.Reason = "授权码绑定的机器与当前设备不一致"
		return base
	}

	// 时间回拨检测：当前时间明显早于历史最大观测时间，说明系统时钟
	// 被调回去了。此时不直接判无效（可能是用户刚改完时区），而是要求
	// 时钟回到正常后再判定——返回 invalid 会把「改错时区」的用户锁死。
	if record.LastSeenAt > 0 && now.Before(time.Unix(record.LastSeenAt, 0).Add(-ClockSkewTolerance)) {
		base.State = StateInvalid
		base.Reason = fmt.Sprintf(
			"系统时间异常（当前时间早于上次启动时间 %s），请校准系统时钟后重试",
			time.Unix(record.LastSeenAt, 0).Format("2006-01-02 15:04"))
		return base
	}

	// 永久授权：跳过时间判定，但仍需校验功能位图存在。
	if payload.IsPermanent() {
		base.State = StateValid
		base.Features = payload.Feat
		return base
	}

	exp := time.Unix(payload.EXP, 0)
	remaining := remainingDays(now, exp)
	base.RemainingDays = remaining

	switch {
	case now.Before(exp):
		base.State = StateValid
		base.Features = payload.Feat
	case now.Before(exp.Add(GracePeriod)):
		base.State = StateGrace
		base.Features = payload.Feat
		base.Reason = fmt.Sprintf("授权已于 %s 过期，进入 %d 天宽限期",
			exp.Format("2006-01-02"), int(GracePeriod.Hours()/24))
	default:
		base.State = StateReadOnly
		base.Features = restrictFeatures(payload.Feat, readOnlyFeatures)
		base.Reason = fmt.Sprintf("授权已于 %s 过期，当前为只读模式",
			exp.Format("2006-01-02"))
	}
	return base
}

// CheckRollback 判断新导入的授权码是否比已保存的码更早到期。
//
// 单独拆出来的原因：导入流程需要在**保存之前**拦住回滚，而不是保存后
// 再发现不对——后者会让本地存着一份已经用不了的码，用户看到的状态反而
// 更糟。
// RollbackError 说明一张授权码为什么被判为回滚，并带上两个日期。
//
// 带上日期不是为了好看：本次事故里用户看到的是一句「到期时间早于本机
// 已用过的授权」，既不知道「已用过的」是哪天，也不知道该签多晚才能过。
// 把两个日期直接摆出来，运维一眼就能算出要签多少天。
type RollbackError struct {
	// Horizon 是本机记录过的有效期上限（Unix 秒）。
	Horizon int64
	// Incoming 是新码的到期时间（Unix 秒）。
	Incoming int64
}

func (e *RollbackError) Error() string {
	return fmt.Sprintf("%v: incoming expires %s, local horizon is %s",
		ErrRollback,
		time.Unix(e.Incoming, 0).Format("2006-01-02"),
		time.Unix(e.Horizon, 0).Format("2006-01-02"))
}

// Is 让 errors.Is(err, ErrRollback) 继续成立。
//
// 调用方历来按哨兵值分支，改成结构体错误后不能破坏这个约定——
// 否则所有 `errors.Is(err, ErrRollback)` 会静默失效，错误落到 default
// 分支，用户看到的提示从「无法导入」变成「格式不正确」。
func (e *RollbackError) Is(target error) bool { return target == ErrRollback }

// RecordTrusted 判断本地保存的码在**当前公钥**下是否仍然可信。
//
// 这是回滚保护的准入门槛，也是 2026-11 那次事故的修复点。当时的行为是
// 无条件相信 Record.MaxExpSeen，于是发码端换过一次密钥之后，旧码因为
// 验不过签变成 invalid，水位却照样生效，一张验签完全通过的新码被永久
// 拒绝——用户被锁在系统外面，而且没有任何自助出路。
//
// 语义上正确的规则是：水位记的是「这张码给过我的最晚到期日」，只有这张
// 码本身仍然可信，这个事实才成立。码不可信（换钥、损坏、被手改），
// 水位就必须作废。
//
// 注意：这里只判签名，不判到期与机器。一张已经过期的码，其水位仍然是
// 真实发生过的历史，不该因为过期就丢掉——它照样能拦住「拿更早的旧码
// 覆盖」。
func RecordTrusted(record *Record, publicKey []byte) bool {
	if record == nil || trimSpace(record.Code) == "" {
		return false
	}
	_, err := Decode(record.Code, publicKey)
	return err == nil
}

// CheckRollback 判断新导入的授权码是否比已保存的码更早到期。
//
// 单独拆出来的原因：导入流程需要在**保存之前**拦住回滚，而不是保存后
// 再发现不对——后者会让本地存着一份已经用不了的码，用户看到的状态反而
// 更糟。
//
// trusted 表示现存码在当前公钥下是否仍然可信，由 RecordTrusted 算出。
// 现存码不可信时水位不参与判定：那不是「历史事实」，而是一段对不上号的
// 记录，用它拦人只会把用户锁死。
func CheckRollback(existing *Record, incoming Payload, trusted bool) error {
	if existing == nil {
		return nil
	}
	// 永久授权（EXP == 0）视为最晚，永远不算回滚。
	if incoming.IsPermanent() {
		return nil
	}
	// 复位码是发码端主动放弃水位的唯一手段：想清空历史上限，必须持私钥
	// 签一张。客户端不向用户开放任何自助绕过的入口。
	if incoming.RST {
		return nil
	}
	if !trusted {
		return nil
	}
	// MaxExpSeen 记录了历史最晚到期时间，比 existing 当前码更可靠：
	// 用户可能已经用一张短期码覆盖过长期码，此时 existing.Code 里的
	// 到期时间已经变小，但历史峰值仍在。
	horizon := existing.MaxExpSeen
	if horizon > 0 && incoming.EXP < horizon {
		return &RollbackError{Horizon: horizon, Incoming: incoming.EXP}
	}
	return nil
}

// NextRecord 在成功导入一张新码后，算出应当保存的新记录。
//
// 保留历史峰值是这里的关键：MaxExpSeen 取「旧值」与「新码到期时间」的
// 较大者，而不是直接覆盖。否则用户用一张 30 天的临时码覆盖掉 365 天的
// 长期码之后，回滚保护就失效了。
//
// 两个例外会主动丢掉旧峰值：
//   - trusted 为 false：旧记录已经对不上当前公钥，它记的上限不再可信；
//   - incoming.RST：发码端签了复位码，明确要求从这张码重新起算。
//
// 丢掉峰值不丢其他信息：Code、AppliedAt 会换成新码的，KeyID 记下当前
// 签发者，审计里也会留下一条，事后仍能还原发生了什么。
func NextRecord(existing *Record, code string, payload Payload, now time.Time, trusted bool) Record {
	rec := Record{
		Code:      code,
		AppliedAt: now.Unix(),
		KeyID:     payload.KID,
		LastSeenAt: func() int64 {
			if existing != nil && existing.LastSeenAt > now.Unix() {
				return existing.LastSeenAt
			}
			return now.Unix()
		}(),
	}
	rec.MaxExpSeen = payload.EXP
	keepPeak := existing != nil && trusted && !payload.RST
	if keepPeak && existing.MaxExpSeen > rec.MaxExpSeen {
		rec.MaxExpSeen = existing.MaxExpSeen
	}
	return rec
}

// TouchRecord 在每次启动或定期校验时更新「最近一次观测到的系统时间」，
// 用于下一次的时间回拨检测。
//
// 取 max 而不是直接覆盖：攻击者可以每次启动把时钟往回拨一点点，
// 若每次都覆盖成当前（较小的）时间，回拨就永远检测不出来。
func TouchRecord(record *Record, now time.Time) {
	if record == nil {
		return
	}
	if now.Unix() > record.LastSeenAt {
		record.LastSeenAt = now.Unix()
	}
}

// remainingDays 计算剩余天数，向上取整到天。
//
// 用向上取整是因为用户视角的「还剩 1 天」应该包括最后不满 24 小时的
// 那段：如果实际还剩 3 小时却显示「0 天」，用户会以为已经过期。
func remainingDays(now, exp time.Time) int {
	if exp.Before(now) {
		return 0
	}
	d := exp.Sub(now)
	days := int(d / (24 * time.Hour))
	if d%(24*time.Hour) > 0 {
		days++
	}
	return days
}

// restrictFeatures 把授权位图与允许集合取交集，保持授权码里的原始顺序。
func restrictFeatures(granted, allowed []Feature) []Feature {
	if len(granted) == 0 {
		return nil
	}
	allowedSet := make(map[Feature]struct{}, len(allowed))
	for _, f := range allowed {
		allowedSet[f] = struct{}{}
	}
	out := make([]Feature, 0, len(granted))
	for _, f := range granted {
		if _, ok := allowedSet[f]; ok {
			out = append(out, f)
		}
	}
	return out
}

// describeDecodeError 把内部错误翻译成给用户看的中文。
//
// 刻意不区分「签名长度不对」与「签名内容不符」（两者都归到最后一档），
// 也不回显具体的 base64/JSON 错误——那些信息对用户没有价值，对攻击者
// 却是在告诉他离成功还差几步。
func describeDecodeError(err error) string {
	switch {
	case isErr(err, ErrEmptyCode):
		return "尚未导入授权码"
	case isErr(err, ErrBadFormat):
		return "授权码格式不正确：应为 WNL1.<数据>.<签名> 三段"
	case isErr(err, ErrBadEncoding):
		return "授权码内容已损坏，请重新复制完整的授权码"
	case isErr(err, ErrBadPayload):
		return "授权码内容不完整，请联系发码方重新签发"
	case isErr(err, ErrBadSignature):
		return "授权码无效或已被篡改"
	default:
		return "授权码校验失败"
	}
}

// HasFeature 报告这份判定结果里是否包含某个功能位。
//
// 只看 Features 而不看 Payload：过期降级时 Features 已被 restrictFeatures
// 裁剪过，调用方应当以 Features 为准，而不是回头去读原始载荷——
// 否则降级会在调用方手里被悄悄绕过。
func (e Evaluation) HasFeature(f Feature) bool {
	for _, granted := range e.Features {
		if granted == f {
			return true
		}
	}
	return false
}
