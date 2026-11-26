package license

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 文件名与权限。
//
// 用 0600：授权码里含有机器指纹与使用人信息，同一台机器上的其他系统
// 账号没有理由读到它。目录用 0700 同理。
const (
	licenseFileName = "license.json"
	fileMode        = 0o600
	dirMode         = 0o700
)

// Store 负责把 Record 落到磁盘，并对外提供判定入口。
//
// 它是并发安全的：HTTP 请求与 Wails 绑定可能同时触发重新校验，
// 用互斥锁把「读记录 → 判定 → 更新观测时间 → 落盘」这一串包起来，
// 避免两个 goroutine 各自基于旧值写盘、互相覆盖。
type Store struct {
	mu        sync.Mutex
	dir       string
	publicKey []byte
	// localMACs 可注入，测试时用假的 MAC；生产为 nil 表示每次现取。
	// 之所以允许为空时现取而不是缓存在构造时，是因为网卡可能在运行中
	// 插拔，用户开完 Wi-Fi 就能通过校验——缓存会让这个过程变成「重启才生效」。
	localMACs []string
	// now 可注入，测试用。
	now func() time.Time
	// record 是内存中的当前记录；nil 表示尚未加载。
	record *Record
	loaded bool
}

// StoreOptions 是构造函数参数。
type StoreOptions struct {
	// Dir 是授权文件所在目录。调用方传入桌面端的配置目录
	// （便携模式下是 WK_PORTABLE_DIR，否则是用户配置目录）。
	Dir string
	// PublicKey 是内置的 Ed25519 公钥。
	PublicKey []byte
	// LocalMACs 仅用于测试；留空则运行时现取。
	LocalMACs []string
	// Now 仅用于测试；留空则用 time.Now。
	Now func() time.Time
}

// NewStore 创建一个 Store。它不读盘，首次调用 Evaluate 时才加载，
// 这样构造过程不会因为磁盘问题失败，也不会在包初始化阶段碰 IO。
func NewStore(opts StoreOptions) *Store {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		dir:       opts.Dir,
		publicKey: opts.PublicKey,
		localMACs: opts.LocalMACs,
		now:       now,
	}
}

// macs 返回用于判定的 MAC 列表。
func (s *Store) macs() []string {
	if s.localMACs != nil {
		return s.localMACs
	}
	return CollectLocalMACs()
}

// path 返回授权文件路径。
func (s *Store) path() string { return filepath.Join(s.dir, licenseFileName) }

// loadLocked 从磁盘读记录。调用方必须已持有 s.mu。
//
// 文件不存在不算错误（首次启动的正常状态），损坏的文件也不算错误——
// 把它当作「没有授权」处理，但**不删除**它：用户可能手工编辑坏了，
// 留着让他自己看反而有助于排查，而且删除是破坏性操作。
func (s *Store) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	if s.dir == "" {
		return
	}
	data, err := os.ReadFile(s.path())
	if err != nil {
		return
	}
	var rec Record
	if json.Unmarshal(data, &rec) != nil {
		return
	}
	s.record = &rec
}

// saveLocked 原子地写记录。调用方必须已持有 s.mu。
//
// 先写临时文件再 rename：直接覆写的话，进程在写一半时被杀会留下一个
// 半个 JSON，下次启动就解析失败、用户莫名其妙掉授权。rename 在同一
// 文件系统内是原子的。
func (s *Store) saveLocked(rec *Record) error {
	if s.dir == "" {
		return errors.New("license: store directory is not configured")
	}
	if err := os.MkdirAll(s.dir, dirMode); err != nil {
		return fmt.Errorf("license: create directory: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("license: encode record: %w", err)
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, data, fileMode); err != nil {
		return fmt.Errorf("license: write record: %w", err)
	}
	if err := os.Rename(tmp, s.path()); err != nil {
		// rename 失败时清掉临时文件，避免留下垃圾。
		_ = os.Remove(tmp)
		return fmt.Errorf("license: replace record: %w", err)
	}
	return nil
}

// Evaluate 读取当前记录并做一次完整判定，同时把「本次观测时间」写回磁盘。
//
// 每次调用都会推进 LastSeenAt，这是时间回拨检测能生效的前提：只有留下
// 「我来过，当时系统时间是 X」的痕迹，下次才能在 X 之前发现异常。
func (s *Store) Evaluate() Evaluation {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loadLocked()
	now := s.now()
	macs := s.macs()

	if s.record != nil {
		// 先推进观测时间再判定：判定里的回拨检测比较的是「历史最大」，
		// 与本次写入无关，顺序不影响结论；先写可以在判定过程中崩溃时
		// 也不丢失这次观测。
		before := s.record.LastSeenAt
		TouchRecord(s.record, now)
		if s.record.LastSeenAt != before {
			if err := s.saveLocked(s.record); err != nil {
				// 落盘失败不影响本次判定结果，但要留下线索。
				// 用 fmt 而非日志包，是为了让本包不依赖具体日志实现。
				fmt.Fprintf(os.Stderr, "[license] persist observation failed: %v\n", err)
			}
		}
	}

	return Evaluate(s.record, s.publicKey, macs, now)
}

// Current 返回当前判定结果，但不推进 LastSeenAt。
//
// 用于「单纯想知道现在什么状态」的读路径（比如前端轮询授权页），
// 频繁调用不应该产生磁盘写。
func (s *Store) Current() Evaluation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return Evaluate(s.record, s.publicKey, s.macs(), s.now())
}

// AppliedAt 返回当前授权码的导入时间，用于界面上显示「何时激活」。
// 没有授权时返回零值。
func (s *Store) AppliedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if s.record == nil || s.record.AppliedAt == 0 {
		return time.Time{}
	}
	return time.Unix(s.record.AppliedAt, 0)
}

// ApplyOutcome 记录一次成功导入里「值得留档」的副作用。
//
// 导入本身只返回 Evaluation，够界面用；但回滚水位被清空这件事必须留下
// 痕迹——清零水位意味着本机从此接受更早到期的码，属于降低防护强度的
// 动作，审计里没有记录，事后就无法回答「为什么这台机器突然能导旧码」。
type ApplyOutcome struct {
	// KeyID 是本次导入码的签发者指纹；老码没带这个字段，为空。
	KeyID string
	// HorizonFrom 是本次被丢弃的历史水位（Unix 秒），0 表示没丢弃。
	HorizonFrom int64
	// Reason 说明丢弃水位的原因，取值见下面两个常量；空字符串表示未丢弃。
	Reason string
}

// 丢弃水位的原因。分两者是为了排障时能区分「运维主动复位」与
// 「发码端换过密钥导致旧记录失效」——前者是计划内动作，后者是事故征兆。
const (
	// ResetReasonIssuer 表示导入的是复位码（Payload.RST）。
	ResetReasonIssuer = "issuer_reset"
	// ResetReasonUntrusted 表示旧记录在当前公钥下已无法验签。
	ResetReasonUntrusted = "untrusted_record"
)

// KeyID 返回本机内置公钥的指纹。
//
// 授权页与排障都需要它：用户报「码导入不了」时，先让运维比对「码里的
// kid」与「客户端认的 kid」，一眼就能分出是签名对不上还是别的原因。
// 这里只读内存，不加载磁盘。
func (s *Store) KeyID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return KeyID(s.publicKey)
}

// RecordedKeyID 返回已保存授权码记录的签发者指纹。
//
// 老记录没有这个字段，返回空串表示「未知」。它与 KeyID() 不一致，
// 说明这张码是用旧密钥签的——换钥事故的第一现场证据。
func (s *Store) RecordedKeyID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if s.record == nil {
		return ""
	}
	return s.record.KeyID
}

// Apply 导入一张新授权码。
//
// 流程刻意是「先全部校验、再落盘」：
//
//  1. 验签——伪造的码不能进入后续步骤。
//  2. 机器匹配——别台机器的码不该被接受。
//  3. 回滚检查——旧码不能覆盖新码。
//  4. 到期检查——已经过期的码不让导入，否则用户会看到「刚导入就过期」。
//
// 任何一步失败都返回错误且**不修改磁盘**，用户手上仍是原来那张码，
// 不会出现「导入失败反而把好码弄没了」的事故。
func (s *Store) Apply(rawCode string) (Evaluation, error) {
	ev, _, err := s.ApplyWithOutcome(rawCode)
	return ev, err
}

// ApplyWithOutcome 与 Apply 相同，但额外返回需要写审计的副作用。
//
// 分成两个函数而不是直接改 Apply 的签名，是为了不惊动既有调用点：
// 导入成功与否是业务主路径，多数调用方只关心 Evaluation；只有需要
// 留档的调用方（授权服务）才用这个更宽的口子。
func (s *Store) ApplyWithOutcome(rawCode string) (Evaluation, ApplyOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.loadLocked()
	now := s.now()
	macs := s.macs()

	code, err := Decode(rawCode, s.publicKey)
	if err != nil {
		return s.evaluateLocked(now, macs), ApplyOutcome{}, err
	}
	payload := code.Payload

	machineOK := false
	if len(payload.MAC) > 0 {
		machineOK = Matches(payload.MAC, macs)
	} else {
		machineOK = MatchesFingerprint(payload.FP, macs)
	}
	if !machineOK {
		return s.evaluateLocked(now, macs), ApplyOutcome{}, ErrFingerprintMismatch
	}

	// 现存码是否仍然对得上当前公钥，决定了它的历史水位还该不该拦人。
	// 详见 RecordTrusted 的注释：换钥之后旧码验不过签，水位必须作废，
	// 否则用户会被一张自己无法识别的历史记录永久挡在门外。
	trusted := RecordTrusted(s.record, s.publicKey)

	if err := CheckRollback(s.record, payload, trusted); err != nil {
		return s.evaluateLocked(now, macs), ApplyOutcome{}, err
	}

	// 已过期的码不接受导入：签名有效但时间已过，导入它只会让用户
	// 看到「刚激活就过期」，不如直接告诉他找发码方续期。
	if !payload.IsPermanent() && !now.Before(time.Unix(payload.EXP, 0)) {
		return s.evaluateLocked(now, macs), ApplyOutcome{}, fmt.Errorf(
			"%w: this code expired on %s", ErrExpired,
			time.Unix(payload.EXP, 0).Format("2006-01-02"))
	}

	outcome := ApplyOutcome{KeyID: payload.KID}
	if s.record != nil && s.record.MaxExpSeen > 0 && s.record.MaxExpSeen > payload.EXP {
		switch {
		case payload.RST:
			outcome.Reason, outcome.HorizonFrom = ResetReasonIssuer, s.record.MaxExpSeen
		case !trusted:
			outcome.Reason, outcome.HorizonFrom = ResetReasonUntrusted, s.record.MaxExpSeen
		}
	}

	rec := NextRecord(s.record, code.Raw, payload, now, trusted)
	// 先落盘再更新内存：反过来的话，落盘失败会导致内存与磁盘不一致，
	// 重启后授权又变回旧的，用户会以为导入没生效。
	if err := s.saveLocked(&rec); err != nil {
		return s.evaluateLocked(now, macs), ApplyOutcome{}, err
	}
	s.record = &rec
	return Evaluate(s.record, s.publicKey, macs, now), outcome, nil
}

// evaluateLocked 在不推进观测时间的前提下做判定，供 Apply 的错误分支复用。
func (s *Store) evaluateLocked(now time.Time, macs []string) Evaluation {
	return Evaluate(s.record, s.publicKey, macs, now)
}

// Reset 清空本地授权。仅在用户明确要求「移除授权」时调用。
//
// 它会把文件重命名而不是删除：留着备份，万一用户是误操作，技术支持
// 还能帮他从备份里恢复。
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if s.dir == "" {
		return errors.New("license: store directory is not configured")
	}
	src := s.path()
	if _, err := os.Stat(src); err != nil {
		// 没有文件就等于已经清空，不算错误。
		s.record = nil
		return nil
	}
	backup := src + ".backup"
	_ = os.Remove(backup)
	if err := os.Rename(src, backup); err != nil {
		return fmt.Errorf("license: move record aside: %w", err)
	}
	s.record = nil
	return nil
}

// MachineFingerprint 是给前端用的辅助函数：返回当前机器的指纹与 MAC 列表。
//
// 授权页要让用户看到并复制机器码，用这个函数而不是让前端自己算——
// 前端算不了（浏览器拿不到网卡 MAC），只能由本地服务提供。
func MachineFingerprint(localMACs []string) (fingerprint string, macs []string) {
	normalized := NormalizeMACs(localMACs)
	return Fingerprint(normalized), normalized
}

// MachineFingerprint 返回本机指纹与参与计算的 MAC 列表。
//
// 供授权页展示：用户把指纹报给运维，运维据此发码。返回的 MAC 列表是
// 归一化之后的形态，与写进授权码里的完全一致，便于人工逐条核对。
func (s *Store) MachineFingerprint() (fingerprint string, macs []string) {
	if s == nil {
		return MachineFingerprint(CollectLocalMACs())
	}
	s.mu.Lock()
	local := s.localMACs
	s.mu.Unlock()
	return MachineFingerprint(local)
}

// SetNowFunc 替换时间源。仅用于测试「时间推进后」的判定，
// 生产代码不要在运行期调用——热改时间源会让防回拨机制失去意义。
func (s *Store) SetNowFunc(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now != nil {
		s.now = now
	}
}
