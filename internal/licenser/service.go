// Package licenser 负责把 license 包接到 Web 层：授权状态查询、
// 授权码导入、以及所有业务接口共用的准入闸门。
//
// 分层的理由：internal/license 只关心「码本身」，不依赖 gin，也不该
// 知道 HTTP 状态码该怎么选。把 HTTP 语义放在这里，license 包可以单独
// 测试，也方便将来换传输层（比如 CLI 直接校验）。
package licenser

import (
	stderrors "errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/license"
)

// Service 是授权服务：包装 license.Store，并缓存最近一次的判定结果。
//
// 为什么不每次都读盘：打开一个会话页会并发打出几十个接口请求，
// 每次都读 license.json 并推进 LastSeenAt 落盘，会把磁盘写爆。
// 这里用「读操作只用内存快照 + 定时（由调用方触发）刷新」的策略。
type Service struct {
	store *license.Store

	mu     sync.RWMutex
	cached license.Evaluation

	// enabled 为 false 时不启用授权闸门（开发/内部构建）。
	enabled bool

	// checkInterval 是定时重判间隔。
	checkInterval time.Duration

	// 定时重判相关。见 StartWatcher。
	watcherOnce sync.Once
	stopWatcher chan struct{}
	// onStateChange 在状态发生跃迁时被调用，用于审计日志与告警。
	onStateChange func(prev, next license.Evaluation)
	// auditor 写授权审计文件，可为 nil。
	auditor *Auditor
}

// Options 是构造参数。
type Options struct {
	// Store 是用第一期 license.NewStore 构造好的存储。
	Store *license.Store
	// Enabled 表示是否真的启用闸门。为 false 时所有判定都当作「无限制」。
	Enabled bool
	// CheckInterval 是定时重判的间隔。<=0 时用 DefaultCheckInterval。
	CheckInterval time.Duration
	// Auditor 是授权审计写入器，nil 表示不审计。
	Auditor *Auditor
	// OnStateChange 在授权状态跃迁时回调（prev, next）。
	//
	// 用于接审计日志：授权「突然失效」比「一直没授权」更值得留痕——
	// 前者意味着某台在用机器掉了授权，需要人介入。
	OnStateChange func(prev, next license.Evaluation)
}

// NewService 构造授权服务，并立刻做一次判定填入缓存。
func NewService(opts Options) *Service {
	s := &Service{
		store:         opts.Store,
		enabled:       opts.Enabled,
		checkInterval: opts.CheckInterval,
		onStateChange: opts.OnStateChange,
		auditor:       opts.Auditor,
	}
	if s.checkInterval <= 0 {
		s.checkInterval = DefaultCheckInterval
	}
	if s.store != nil {
		s.cached = s.store.Evaluate()
	}
	return s
}

// NewFailed 构造「闸门启用、但无法校验授权」的失败态服务。
//
// 用于公钥损坏、授权目录不可用这类硬错误。这类错误绝不能退化成
// 「不启用授权」：否则任何一次打包失误（比如 public.key 写坏）
// 都会静默把授权保护关掉，而日志里只有一行警告。
//
// 失败态服务的行为：状态固定为 invalid 并带上原因，所有业务接口
// 被 Gate 拒绝，只放行登录与授权页——用户能看到「为什么用不了」，
// 运维也能从状态接口与日志里拿到原因。
//
// 注意与「没有配置公钥」区分：那是设计内的不启用，调用方应返回 nil，
// 而不是失败态服务。
func NewFailed(reason string) *Service {
	if strings.TrimSpace(reason) == "" {
		reason = "授权校验不可用"
	}
	return &Service{
		enabled:       true,
		checkInterval: DefaultCheckInterval,
		cached: license.Evaluation{
			State:  license.StateInvalid,
			Reason: reason,
		},
	}
}

// Enabled 报告闸门是否启用。
//
// 只认 enabled 标志，不再要求 store 非空：失败态服务刻意没有 store
// （无法读盘校验），但它必须仍然是「启用」的，才能把请求拦住。
func (s *Service) Enabled() bool {
	return s != nil && s.enabled
}

// Status 返回当前授权状态（不推进观测时间，不落盘）。
func (s *Service) Status() license.Evaluation {
	if s == nil {
		return license.Evaluation{State: license.StateMissing, Reason: "授权服务未初始化"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cached.State == "" {
		// 未走 NewService / NewFailed 的零值：按「未初始化」处理，
		// 不要把它当成 valid。
		return license.Evaluation{State: license.StateMissing, Reason: "授权服务未初始化"}
	}
	return s.cached
}

// Refresh 重新读盘判定一次，并把结果写回缓存。
//
// 调用时机：启动时、导入授权码后、以及长时间运行后的定期校验。
// 不放在每个请求上，原因见 Service 的注释。
func (s *Service) Refresh() license.Evaluation {
	if s == nil {
		return license.Evaluation{State: license.StateMissing, Reason: "授权服务未初始化"}
	}
	if s.store == nil {
		// 失败态服务没有 store：重判没有意义，保持失败原因，
		// 否则定时器每小时会把它改写成「无授权」，把真正的原因冲掉。
		return s.Status()
	}
	ev := s.store.Evaluate()
	s.mu.Lock()
	s.cached = ev
	s.mu.Unlock()
	return ev
}

// Apply 导入一张授权码。
//
// 返回的错误区分了「码本身有问题」和「这台机器不适用」，因为两者对
// 用户的含义完全不同：前者要找运维换码，后者说明码发错机器了。
func (s *Service) Apply(code string) (license.Evaluation, error) {
	if s == nil {
		return license.Evaluation{}, errors.NewInternalServerError("授权服务未初始化")
	}
	if s.store == nil {
		// 失败态服务：没有校验能力，导入注定失败。给出原因，
		// 避免用户反复粘贴一张永远验不过的码。
		return license.Evaluation{}, errors.NewForbiddenError(
			"授权服务不可用：" + s.Status().Reason)
	}
	ev, outcome, err := s.store.ApplyWithOutcome(code)
	if err != nil {
		var appErr *errors.AppError
		switch {
		case stderrors.Is(err, license.ErrFingerprintMismatch):
			appErr = errors.NewForbiddenError(
				"这张授权码不是发给本机的").WithDetails(err.Error())
		case stderrors.Is(err, license.ErrExpired):
			appErr = errors.NewForbiddenError(
				"这张授权码已经过期").WithDetails(err.Error())
		case stderrors.Is(err, license.ErrRollback):
			// 报错必须带上两侧日期，否则用户只知道「被拒了」，运维也不知道
			// 该签多晚才能过——本次事故里这条提示把人卡在了半路上。
			msg := "这张授权码的到期时间早于本机已用过的授权，无法导入"
			var rb *license.RollbackError
			if stderrors.As(err, &rb) {
				msg = fmt.Sprintf(
					"这张授权码于 %s 到期，早于本机已用过的授权（%s），无法导入。"+
						"请联系发码方重签一张不早于 %s 的码",
					time.Unix(rb.Incoming, 0).Format("2006-01-02"),
					time.Unix(rb.Horizon, 0).Format("2006-01-02"),
					time.Unix(rb.Horizon, 0).Format("2006-01-02"))
			}
			appErr = errors.NewForbiddenError(msg).WithDetails(err.Error())
		case stderrors.Is(err, license.ErrBadSignature):
			appErr = errors.NewValidationError(
				"授权码签名校验失败，请确认复制完整且未被修改").WithDetails(err.Error())
		default:
			appErr = errors.NewValidationError(
				"授权码格式不正确").WithDetails(err.Error())
		}
		// 失败也要留痕：审计的价值恰恰在「为什么被拒」。
		auditRejected(s.auditor, appErr.Message, code)
		return license.Evaluation{}, appErr
	}

	s.mu.Lock()
	prev := s.cached
	s.cached = ev
	s.mu.Unlock()

	auditApplied(s.auditor, ev, code)
	// 水位被清空是一次降低防护强度的动作，必须留档：事后要能回答
	// 「这台机器为什么突然能导一张更早到期的码」。
	auditHorizonReset(s.auditor, outcome, ev, code)
	// 导入成功本身就是一次跃迁（比如 missing -> valid），
	// 需要触发回调，否则前端与日志都感知不到「刚激活」这件事。
	if s.onStateChange != nil && prev.State != ev.State {
		s.onStateChange(prev, ev)
	}
	return ev, nil
}

// KeyID 返回本机内置公钥的指纹。
//
// 与 RecordedKeyID 一起暴露给前端，是为了让「换钥事故」在界面上一眼
// 可见：两个值不一致，就说明现有那张码不是本机这把公钥签的。以前这种
// 情况只能表现为一句「授权码无效或已被篡改」，把用户引向完全错误的
// 方向（以为码被改过）。
func (s *Service) KeyID() string {
	if s == nil || s.store == nil {
		return ""
	}
	return s.store.KeyID()
}

// RecordedKeyID 返回已存授权码记录的签发者指纹，空串表示未知（老码）。
func (s *Service) RecordedKeyID() string {
	if s == nil || s.store == nil {
		return ""
	}
	return s.store.RecordedKeyID()
}

// MachineFingerprint 返回本机指纹与参与计算的 MAC 列表，供授权页展示，
// 用户拿它去找运维要码。
func (s *Service) MachineFingerprint() (string, []string) {
	if s == nil || s.store == nil {
		fp, macs := license.MachineFingerprint(license.CollectLocalMACs())
		return fp, macs
	}
	return s.store.MachineFingerprint()
}

// 读接口路径。这些路径在「无授权」时仍然放行，否则用户连授权页都打不开。
//
// 这份清单只负责「请求能不能到达处理器」。注意 login / auto-setup 虽然
// 在这里，但处理器内部还会再判一次「能不能签发令牌」——放行请求与
// 允许登录是两件事：前者要让用户看到「本机未授权」的明确提示，
// 后者必须等授权到位。判定见 AllowSessionIssue。
var readOnlyAllowlist = []string{
	"/api/v1/license",          // 授权状态 / 机器指纹
	"/api/v1/auth/login",       // 登录：先能进系统，再看得到授权页
	"/api/v1/auth/auto-setup",  // 桌面端免密建号，同上
	"/api/v1/auth/config",      // 登录页需要读配置才知道走哪种登录方式
	"/api/v1/auth/refresh",     // 刷新令牌
	"/api/v1/auth/logout",      // 登出永远放行，否则无授权时无法退出
	"/api/v1/auth/me",          // 前端恢复登录态要用
	"/api/v1/auth/oidc/config", // 登录页的第三方登录入口
	"/health",                  // 健康检查
}

// IsAllowedPath 判断这个路径在「无授权」状态下是否仍可访问。
//
// 用严格相等而不是前缀匹配：这七个路径都不是资源树（没有子资源），
// 前缀匹配会把将来新加的 /api/v1/license/export 之类接口一并放到
// 闸门外，而这种「加了接口忘了收紧白名单」的漏洞，事后很难发现。
func IsAllowedPath(path string) bool {
	for _, allowed := range readOnlyAllowlist {
		if path == allowed {
			return true
		}
	}
	return false
}

// 功能位到接口前缀的映射。
//
// 这张表决定「哪个功能位保护哪组接口」。两条规则：
//
//  1. 每个前缀只能出现一次。同一前缀映射到两个功能位会让「先命中者
//     胜出」，出现「给了知识库权限却被要求 OCR 权限」的怪结果——
//     这是最容易犯的错误，因此下面按前缀分组、功能位取并集语义。
//
//  2. 生成类接口要**先于**读接口匹配（用 deny 标记排在最前），
//     否则 /api/v1/artifacts 会被更宽的前缀吃掉。
var featurePrefixes = []struct {
	feature license.Feature
	prefix  string
}{
	// 文档生成：产物库与技能（技能即 PPT/Word/Excel 生成能力）。
	{license.FeatureOffice, "/api/v1/artifacts"},
	{license.FeatureOffice, "/api/v1/skills"},
	// 知识库：读写知识库本身。OCR 是知识库的解析手段，
	// 因此不单独占一条前缀，改用 RequiredFeature 的多功能位语义。
	{license.FeatureKB, "/api/v1/knowledge-bases"},
	{license.FeatureKB, "/api/v1/knowledge"},
	// 对话。
	{license.FeatureChat, "/api/v1/agent-chat"},
	{license.FeatureChat, "/api/v1/knowledge-chat"},
	// /api/v1/sessions 刻意不在这里：列出、重命名、删除会话属于
	// 「管理自己的数据」，与「能不能发起新的问答」是两件事。
	// 只买知识库不买对话的客户，仍应能看到自己的历史会话记录；
	// 真正消耗模型算力的是上面两条 chat 路径。
}

// ocrPaths 是知识库里真正需要 OCR 的那几个接口。
//
// 单独列出来而不是让整个 /api/v1/knowledge-bases 都要求 OCR：
// 没买 OCR 的客户仍然要能建库、传 txt/md、检索；把他挡在库外面
// 等于把这个功能位变成了「知识库总开关」，和 KB 位重复。
var ocrPaths = []string{
	"/api/v1/knowledge/image",
	"/api/v1/knowledge/ocr",
}

// RequiredFeature 返回访问该路径所需的功能位。
//
// 第二个返回值为 false 表示这张表不覆盖该路径（不据此拒绝）。
func RequiredFeature(path string) (license.Feature, bool) {
	// OCR 专用接口优先判断：它们也在知识库前缀下，
	// 若先走下面的循环会被 KB 位吞掉。
	for _, p := range ocrPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return license.FeatureOCR, true
		}
	}
	for _, rule := range featurePrefixes {
		if path == rule.prefix || strings.HasPrefix(path, rule.prefix+"/") {
			return rule.feature, true
		}
	}
	return "", false
}

// GateDecision 是准入判定的结果。
type GateDecision struct {
	// Allowed 为 false 时调用方应当直接拒绝请求。
	Allowed bool
	// Status 是建议的 HTTP 状态码。
	Status int
	// Code 是机器可读的原因码，前端据此决定跳到哪个页面。
	Code string
	// Message 是给用户看的中文说明。
	Message string
}

// 原因码。前端用它们判断该弹授权页还是提示功能受限。
const (
	CodeLicenseMissing = "LICENSE_MISSING"
	CodeLicenseInvalid = "LICENSE_INVALID"
	CodeLicenseExpired = "LICENSE_EXPIRED"
	CodeFeatureDenied  = "LICENSE_FEATURE_DENIED"
)

// Gate 判断这个请求该不该放行。
//
// 这个函数是所有闸门的单一出口：中间件、登录、免密建号都调它，
// 保证三处判定逻辑永远一致——这是第一期方案里最强调的一点，
// 因为「只挂登录、漏了免密建号」正是最容易出的绕过。
func (s *Service) Gate(path string) GateDecision {
	if !s.Enabled() {
		return GateDecision{Allowed: true}
	}

	// 授权与登录相关的路径永远放行：否则用户既进不了系统，
	// 也看不到授权页，形成死锁。
	if IsAllowedPath(path) {
		return GateDecision{Allowed: true}
	}

	ev := s.Status()
	switch ev.State {
	case license.StateValid, license.StateGrace:
		// 正常与宽限期都可以跑满功能；宽限期只是提醒用户续期。
		// 落在 valid/grace 之外的状态不检查功能位，直接拒绝。
		if feature, ok := RequiredFeature(path); ok && !ev.HasFeature(feature) {
			return GateDecision{
				Allowed: false,
				Status:  403,
				Code:    CodeFeatureDenied,
				Message: "当前授权未包含「" + FeatureLabel(feature) + "」功能。",
			}
		}
		return GateDecision{Allowed: true}

	case license.StateReadOnly:
		// 过期降级：只关掉「生成类」能力，其余全部放行。
		//
		// 这里用负面清单而不是只读白名单：白名单要枚举几十个接口，
		// 漏一个就让老用户突然打不开自己的资料；而生成类接口就那么
		// 几组，漏一个的后果只是过期后某个功能仍可用，代价小得多。
		if feature, ok := RequiredFeature(path); ok && feature == license.FeatureOffice {
			return GateDecision{
				Allowed: false,
				Status:  403,
				Code:    CodeLicenseExpired,
				Message: "授权已过期，文档生成功能已停用，续期后可恢复。已有资料仍可查看。",
			}
		}
		return GateDecision{Allowed: true}

	default:
		// missing / invalid：什么都没有，业务接口一律不放行。
		return GateDecision{
			Allowed: false,
			Status:  403,
			Code:    CodeLicenseMissing,
			Message: describeBlock(ev),
		}
	}
}

// describeBlock 生成「无授权」时的提示语，尽量带上可操作的信息。
func describeBlock(ev license.Evaluation) string {
	if ev.Reason != "" {
		return "本机尚未获得有效授权：" + ev.Reason + "。请在授权页导入授权码。"
	}
	return "本机尚未获得有效授权，请在授权页导入授权码。"
}

// FeatureLabel 返回功能位的中文名，用于提示语。
func FeatureLabel(f license.Feature) string {
	switch f {
	case license.FeatureKB:
		return "知识库"
	case license.FeatureChat:
		return "对话问答"
	case license.FeatureOCR:
		return "OCR 识别"
	case license.FeatureOffice:
		return "文档生成"
	default:
		return string(f)
	}
}

// HasFeature 报告当前授权是否包含某功能位（不启用的构建设为全部包含）。
func (s *Service) HasFeature(f license.Feature) bool {
	if !s.Enabled() {
		return true
	}
	return s.Status().HasFeature(f)
}

// AllowSessionIssue 判断当前是否允许签发登录令牌。
//
// 与 Gate 的区别：Gate 回答「这个请求能不能到达处理器」，本方法回答
// 「能不能给他一个已登录身份」。分开的原因见 readOnlyAllowlist 的注释：
// 登录接口必须可访问（否则用户看不到未授权提示），但无授权时不能真登录。
//
// 返回空 code 表示允许；否则返回给用户看的中文说明。
func (s *Service) AllowSessionIssue() (code, message string) {
	if !s.Enabled() {
		return "", ""
	}
	ev := s.Status()
	switch ev.State {
	case license.StateValid, license.StateGrace, license.StateReadOnly:
		// 过期降级仍允许登录：用户要能进来看自己的资料。
		return "", ""
	default:
		return CodeLicenseMissing, describeBlock(ev)
	}
}

// DefaultCheckInterval 是授权状态定时重判的默认间隔。
//
// 为什么是 1 小时而不是几分钟：授权到期是「天」级事件，判早判晚一小时
// 对用户体验几乎没有区别；而每秒都在写的 LastSeenAt 会让机械硬盘上的
// 用户听到风扇声。1 小时足以让「过期降级」在一个工作日之内生效。
const DefaultCheckInterval = time.Hour

// StartWatcher 启动后台定时重判，直到 StopWatcher 被调用。
//
// 为什么必须有这个：授权状态原本只在「启动」与「导入新码」时计算。
// 桌面端用户习惯让应用开着好几周，于是会出现「授权码三个月前就到期了，
// 程序却一直在用」——这在商业上等于白送，也与「过期降级」的承诺不符。
//
// 重复调用是安全的（sync.Once 语义），闸门未启用时直接返回。
func (s *Service) StartWatcher() {
	if !s.Enabled() {
		return
	}
	s.watcherOnce.Do(func() {
		s.stopWatcher = make(chan struct{})
		go s.watchLoop(s.stopWatcher)
	})
}

// StopWatcher 停止后台重判。进程退出时调用，避免 goroutine 泄漏告警。
func (s *Service) StopWatcher() {
	if s == nil {
		return
	}
	s.mu.Lock()
	ch := s.stopWatcher
	s.stopWatcher = nil
	s.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (s *Service) watchLoop(stop <-chan struct{}) {
	interval := s.checkInterval
	if interval <= 0 {
		interval = DefaultCheckInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// tick 做一次重判，并在状态跃迁时触发回调。
//
// 单独抽出来是为了能在测试里直接调，不必等真实 ticker。
func (s *Service) tick() {
	if !s.Enabled() {
		return
	}
	prev := s.Status()
	next := s.Refresh()
	// 只在「状态类别」变化时回调，不在每次重判都回调。
	//
	// 原因：valid → valid 每小时都会发生，若也回调会淹没真正有意义的事件。
	// 但剩余天数变化不回调会漏掉「grace 剩余 1 天」这类预警——所以
	// grace 状态下额外按 remainingDays 变化触发一次。
	if prev.State != next.State || (next.State == license.StateGrace && prev.RemainingDays != next.RemainingDays) {
		auditTransition(s.auditor, prev, next)
		if s.onStateChange != nil {
			s.onStateChange(prev, next)
		}
	}
}
