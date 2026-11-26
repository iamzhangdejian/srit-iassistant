package licadmin

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// IssueRequest 是「签一张码」的输入。
type IssueRequest struct {
	// MACs 是要绑定的 MAC 地址，支持三种写法混用。
	MACs []string
	// Days 是有效天数，0 表示永久。
	Days int
	// User 是使用人。
	User string
	// Org 是使用单位。
	Org string
	// Features 是功能位图，为空表示全开。
	Features []license.Feature
	// Serial 可指定序列号；留空自动生成。
	Serial string
	// Note 是备注。
	Note string
	// Force 表示即使发现该机器已有有效授权码也照样签发。
	//
	// 默认行为是拒绝并提示「这台机器已经有码了」。因为同一台机器两张
	// 有效码是纯负担：用户不知道填哪张，客服也分不清哪张是新的。
	// 但换机、重装、码损坏这些场景确实需要第二张，所以留了显式开关。
	Force bool
	// ParentID 指向续期前的旧码，0 表示全新签发。
	//
	// 只有 Renew 会自动填它；手工发码时段意不开放，避免把不相干的两张
	// 码串成一条链。
	ParentID int64
	// Reset 表示签发的是「复位码」：客户端导入时丢弃历史到期水位。
	//
	// 只在一种情况下需要：客户的本地水位被一张到期很远、但现在已经
	// 不可信的码顶高了，而要救他的新码到期更早。复位码把清水位的权力
	// 留在发码端——客户端不提供任何自助绕过入口。
	Reset bool
	// ExpiresAt 是显式到期时间（Unix 秒），0 表示按 Days 计算。
	//
	// 续期要用它：续期的起点是「原到期日」而不是「今天」，用 Days
	// 反推会差出一天（AddDate 与秒数换算不总是可逆）。
	ExpiresAt int64
}

// Service 是发码端的业务入口。
type Service struct {
	store      *Store
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	// now 可注入，测试用。
	now func() time.Time
}

// ServiceOptions 是构造函数参数。
type ServiceOptions struct {
	Store *Store
	// PrivateKeyPath 是私钥文件路径（base64 文本）。
	PrivateKeyPath string
	// Now 仅测试用。
	Now func() time.Time
}

// NewService 加载私钥并构造服务。
//
// 私钥缺失时直接返回错误而不是「以只读模式启动」：一个不能签发的发码端
// 毫无用处，让它启动成功只会让人误以为在正常工作。
func NewService(opts ServiceOptions) (*Service, error) {
	priv, err := LoadPrivateKey(opts.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:      opts.Store,
		privateKey: priv,
		publicKey:  priv.Public().(ed25519.PublicKey),
		now:        now,
	}, nil
}

// LoadPrivateKey 读取 base64 编码的 Ed25519 私钥。
func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("licadmin: read private key %s: %w", path, err)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("licadmin: private key is not base64: %w", err)
	}
	if len(data) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("licadmin: private key must be %d bytes, got %d",
			ed25519.PrivateKeySize, len(data))
	}
	return ed25519.PrivateKey(data), nil
}

// PublicKeyBase64 返回可内置进客户端的公钥文本。
//
// 管理页面要显示它，因为客户端构建时需要把公钥写进代码——这是部署
// 流程里的一个易错环节（换密钥忘了换客户端公钥，用户会看到「授权码
// 无效」）。把它摆在显眼位置能减少这类事故。
func (s *Service) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(s.publicKey)
}

// PublicKeyID 返回公钥指纹，供页面展示与人工比对。
func (s *Service) PublicKeyID() string {
	return license.KeyID(s.publicKey)
}

// IssueResult 是一次签发的结果。
type IssueResult struct {
	Entry Entry `json:"entry"`
	// Warning 是「已经签发但仍需提醒」的信息，例如同一台机器已有旧码。
	Warning string `json:"warning,omitempty"`
}

// Issue 签发一张授权码并记入台账。
func (s *Service) Issue(req IssueRequest) (IssueResult, error) {
	macs := license.NormalizeMACs(req.MACs)
	if len(macs) == 0 {
		return IssueResult{}, errors.New("没有解析出合法的 MAC 地址")
	}
	if req.Days < 0 {
		return IssueResult{}, errors.New("有效天数不能为负")
	}
	features, err := resolveFeatures(req.Features)
	if err != nil {
		return IssueResult{}, err
	}

	now := s.now()
	var warning string
	if !req.Force {
		if existing := s.store.FindActiveByMACs(macs, now); len(existing) > 0 {
			return IssueResult{}, fmt.Errorf(
				"这台机器已有 %d 张有效授权码（最新的序列号 %s，至 %s 到期）。"+
					"如需重签请先作废旧码，或勾选「强制签发」",
				len(existing), existing[0].Serial, existing[0].ExpiresText())
		}
	}

	var exp int64
	if req.ExpiresAt > 0 {
		// 续期走这条路：到期日由调用方（Renew）按原到期日顺延算好。
		exp = req.ExpiresAt
	} else if req.Days > 0 {
		exp = now.AddDate(0, 0, req.Days).Unix()
	}

	serial := strings.TrimSpace(req.Serial)
	if serial == "" {
		serial = s.newSerial(now, macs)
	}

	payload := license.Payload{
		V:    1,
		SN:   serial,
		FP:   license.Fingerprint(macs),
		MAC:  macs,
		IAT:  now.Unix(),
		EXP:  exp,
		Feat: features,
		Org:  strings.TrimSpace(req.Org),
		User: strings.TrimSpace(req.User),
		// 把签发者指纹写进码里：客户端验签失败时可以直接对照
		// 「码是谁签的 / 本机认谁」，不用再靠人工比对公钥文件。
		KID: license.KeyID(s.publicKey),
		RST: req.Reset,
	}
	code, err := license.Encode(payload, s.privateKey)
	if err != nil {
		return IssueResult{}, fmt.Errorf("签发失败：%w", err)
	}

	// 签发后立刻用公钥验一遍。理论上不会失败（同一个密钥对），但发码端
	// 一旦出错，代价是用户拿着废码来回折腾几轮——这个自检很便宜。
	if _, err := license.Decode(code, s.publicKey); err != nil {
		return IssueResult{}, fmt.Errorf("自检失败，未写入台账：%w", err)
	}

	entry, err := s.store.Add(Entry{
		Serial:      serial,
		Code:        code,
		Fingerprint: payload.FP,
		MACs:        macs,
		User:        payload.User,
		Org:         payload.Org,
		Features:    features,
		IssuedAt:    now.Unix(),
		ExpiresAt:   exp,
		Status:      StatusActive,
		Note:        strings.TrimSpace(req.Note),
		ParentID:    req.ParentID,
		KeyID:       payload.KID,
	})
	if err != nil {
		return IssueResult{}, err
	}
	return IssueResult{Entry: entry, Warning: warning}, nil
}

// RenewRequest 是续期请求。
type RenewRequest struct {
	// Days 是本次续多少天。
	Days int
	// Note 写给台账的备注，留空时自动填「N 天续期（自 <原到期日>）」。
	Note string
}

// Renew 为一张现有授权码续期：新签一张、旧码作废，两张由 ParentID 串成链。
//
// 为什么续期不是「改旧记录的到期时间」：旧码已经发给客户了，签名封在
// 码里的到期时间改不了；把台账里的到期日改了只会让管理端和客户端对不
// 上。所以续期的本质是「用同一台机器、同样的功能再签一张更晚到期的
// 码」，客户拿新码覆盖旧码即可。
func (s *Service) Renew(id int64, req RenewRequest) (IssueResult, error) {
	old, ok := s.store.Get(id)
	if !ok {
		return IssueResult{}, fmt.Errorf("台账里没有 ID 为 %d 的记录", id)
	}
	if old.Status == StatusRevoked {
		return IssueResult{}, errors.New("这张码已作废，不能再续期；请直接新签一张")
	}
	if old.ExpiresAt == 0 {
		return IssueResult{}, errors.New("这张是永久授权，没有到期日，无需续期")
	}
	if req.Days <= 0 {
		return IssueResult{}, errors.New("续期天数必须大于 0")
	}

	now := s.now()
	// 未到期时从原到期日顺延，已过期则从今天算起：这样「提前续」不会
	// 白白吞掉剩余天数，「过期补续」也不会凭空多送一段。
	base := now
	if exp := time.Unix(old.ExpiresAt, 0); exp.After(now) {
		base = exp
	}
	exp := base.AddDate(0, 0, req.Days).Unix()

	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = fmt.Sprintf("%d 天续期（自 %s 起算）", req.Days, base.Format("2006-01-02"))
	}

	res, err := s.Issue(IssueRequest{
		// 旧码此刻通常仍在有效期内，不加 Force 会被「同机已有有效码」挡住。
		Force:     true,
		ParentID:  old.ID,
		ExpiresAt: exp,
		MACs:      old.MACs,
		User:      old.User,
		Org:       old.Org,
		Features:  old.Features,
		Note:      note,
	})
	if err != nil {
		return IssueResult{}, err
	}
	if _, err := s.store.RevokeWithReason(
		old.ID,
		"已续期",
		fmt.Sprintf("续期至 %s", res.Entry.Serial),
		now,
	); err != nil {
		// 新码已经落库，这里失败只影响台账整洁；把序列号告诉调用方，
		// 运维可以手工处理，比整笔回滚（丢掉刚签的码）更安全。
		return res, fmt.Errorf("新码已签发（%s），但旧码作废失败：%w", res.Entry.Serial, err)
	}
	return res, nil
}

// BatchRow 是批量导入里的一行。
type BatchRow struct {
	// Index 是原始行号（从 1 开始），用于回报「第 3 行有问题」。
	Index int `json:"index"`
	// Raw 是原始文本，回显给用户便于核对。
	Raw string `json:"raw"`
	// Err 非空表示这一行没通过校验。
	Err string `json:"err,omitempty"`
	// Entry 是成功签发的记录。
	Entry   *Entry `json:"entry,omitempty"`
	Warning string `json:"warning,omitempty"`
}

// BatchResult 是批量导入的结果。
type BatchResult struct {
	Rows []BatchRow `json:"rows"`
	// Issued 是成功条数。
	Issued int `json:"issued"`
	// Failed 是失败条数。
	Failed int `json:"failed"`
}

// ParseBatch 解析批量导入的文本。
//
// 支持 CSV（逗号或制表符分隔），列顺序：
//
//	mac [, user [, org [, days [, features]]]]
//
// 第一行是表头时自动跳过。空行忽略。也允许一行里写多个 MAC，
// 用分号分隔（CSV 的逗号已经被列占用了）。
func ParseBatch(text string) []BatchInputRow {
	var out []BatchInputRow
	seenData := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := splitBatchLine(trimmed)
		// 表头识别看「是不是第一条有效数据」，而不是行号 == 0：
		// 用户经常在表头上面写一行注释或留一个空行，用行号判断会把
		// 表头当成数据行，然后报「MAC 地址不合法」——错得莫名其妙。
		if !seenData && looksLikeHeader(fields) {
			seenData = true
			continue
		}
		seenData = true
		out = append(out, BatchInputRow{Index: i + 1, Raw: trimmed, Fields: fields})
	}
	return out
}

// BatchInputRow 是解析出来的一行原始输入。
type BatchInputRow struct {
	Index  int
	Raw    string
	Fields []string
}

// splitBatchLine 按制表符优先、其次逗号切分。
//
// 先判制表符是刻意的：从 Excel 直接粘贴过来是制表符分隔，而里面可能
// 含有写法的逗号（比如备注）。用逗号切会把整行切碎。
func splitBatchLine(line string) []string {
	sep := ","
	if strings.Contains(line, "\t") {
		sep = "\t"
	}
	parts := strings.Split(line, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func looksLikeHeader(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	first := strings.ToLower(strings.TrimSpace(fields[0]))
	if first == "" {
		return false
	}
	// 判据是「第一列看起来像不像 MAC」，而不是「里面有没有 mac 字样」——
	// 后者会被 `;badmac` 这类尾巴误导，把真正要解析的数据行当表头跳过。
	// 单个字段本身就含分隔符（`a;b`）时，按第一个 MAC 形态判断。
	if idx := strings.IndexAny(first, ";|"); idx >= 0 {
		first = strings.TrimSpace(first[:idx])
	}
	if _, ok := normalizeMACForHeader(first); ok {
		return false
	}
	return strings.Contains(first, "mac") ||
		strings.Contains(first, "地址") ||
		strings.Contains(first, "网卡") ||
		strings.Contains(first, "机器")
}

// IssueBatch 批量签发。
//
// 单行失败不影响其他行——这是批量导入的核心要求。用户粘 50 行进来，
// 其中 3 行的 MAC 写错了，正确的做法是签出 47 张并列出 3 行的问题，
// 而不是整批回滚让他自己找错。
func (s *Service) IssueBatch(text string, defaults IssueRequest) BatchResult {
	inputs := ParseBatch(text)
	res := BatchResult{Rows: make([]BatchRow, 0, len(inputs))}
	for _, in := range inputs {
		row := BatchRow{Index: in.Index, Raw: in.Raw}
		if len(in.Fields) == 0 || in.Fields[0] == "" {
			row.Err = "缺少 MAC 地址"
			res.Failed++
			res.Rows = append(res.Rows, row)
			continue
		}

		// 一行里可能有多个 MAC，用分号分隔。
		rawMACs := strings.Split(in.Fields[0], ";")
		macs := license.NormalizeMACs(rawMACs)
		if len(macs) == 0 {
			row.Err = fmt.Sprintf("MAC 地址不合法：%q", in.Fields[0])
			res.Failed++
			res.Rows = append(res.Rows, row)
			continue
		}
		// 有任何一个 MAC 解析失败就要报出来：用户以为绑了三块网卡，
		// 实际只绑上两块，换机时会被判「机器不匹配」。
		if len(macs) != countNonEmpty(rawMACs) {
			row.Err = fmt.Sprintf("部分 MAC 地址不合法：%q（已解析 %d/%d）",
				in.Fields[0], len(macs), countNonEmpty(rawMACs))
			res.Failed++
			res.Rows = append(res.Rows, row)
			continue
		}

		req := defaults
		req.MACs = macs
		req.Serial = ""
		req.Force = false
		// 逐列覆盖默认值。
		if len(in.Fields) > 1 && in.Fields[1] != "" {
			req.User = in.Fields[1]
		}
		if len(in.Fields) > 2 && in.Fields[2] != "" {
			req.Org = in.Fields[2]
		}
		if len(in.Fields) > 3 && in.Fields[3] != "" {
			if days, err := parseDays(in.Fields[3]); err == nil {
				req.Days = days
			} else {
				row.Err = err.Error()
				res.Failed++
				res.Rows = append(res.Rows, row)
				continue
			}
		}
		if len(in.Fields) > 4 && in.Fields[4] != "" {
			feats, err := parseFeatures(in.Fields[4])
			if err != nil {
				row.Err = err.Error()
				res.Failed++
				res.Rows = append(res.Rows, row)
				continue
			}
			req.Features = feats
		}

		out, err := s.Issue(req)
		if err != nil {
			row.Err = err.Error()
			res.Failed++
			res.Rows = append(res.Rows, row)
			continue
		}
		entry := out.Entry
		row.Entry = &entry
		row.Warning = out.Warning
		res.Issued++
		res.Rows = append(res.Rows, row)
	}
	return res
}

// InspectResult 是「验签自检」的结果。
type InspectResult struct {
	Valid bool `json:"valid"`
	// Entry 是解析出的关键字段，便于客服核对。
	Serial      string            `json:"serial,omitempty"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	MACs        []string          `json:"macs,omitempty"`
	User        string            `json:"user,omitempty"`
	Org         string            `json:"org,omitempty"`
	Features    []license.Feature `json:"features,omitempty"`
	IssuedAt    int64             `json:"issued_at,omitempty"`
	ExpiresAt   int64             `json:"expires_at,omitempty"`
	// Error 是失败原因。
	Error string `json:"error,omitempty"`
	// LedgerID 是台账里对应的记录 ID（0 表示台账里没有这张码）。
	//
	// 拿着码来问的用户，很可能是别处发的、或者早就被删掉的记录——
	// 「台账里没有」本身就是有价值的信息。
	LedgerID int64 `json:"ledger_id,omitempty"`
}

// Inspect 校验一张任意授权码。
//
// 它用的是与客户端完全相同的 license.Decode，这是刻意的：如果管理端
// 另写一套解析，就会出现「页面说有效、客户端说无效」的分裂，而那种
// 分歧最难排查。
func (s *Service) Inspect(code string) InspectResult {
	parsed, err := license.Decode(code, s.publicKey)
	if err != nil {
		return InspectResult{Valid: false, Error: err.Error()}
	}
	p := parsed.Payload
	res := InspectResult{
		Valid:       true,
		Serial:      p.SN,
		Fingerprint: p.FP,
		MACs:        p.MAC,
		User:        p.User,
		Org:         p.Org,
		Features:    p.Feat,
		IssuedAt:    p.IAT,
		ExpiresAt:   p.EXP,
	}
	// 顺手在台账里找一下，帮客服确认这是不是本系统发的。
	for _, e := range s.store.List(Query{Keyword: p.SN}) {
		if e.Serial == p.SN {
			res.LedgerID = e.ID
			break
		}
	}
	return res
}

// ---- 辅助 ----

func (s *Service) newSerial(now time.Time, macs []string) string {
	// 序列号里带日期与首个 MAC 的后四位：客服看序列号就能猜出大概
	// 什么时候发的、绑的哪台机器，不用每次都去查台账。
	suffix := "0000"
	if len(macs) > 0 {
		clean := strings.ReplaceAll(macs[0], ":", "")
		if len(clean) >= 4 {
			suffix = strings.ToUpper(clean[len(clean)-4:])
		}
	}
	base := fmt.Sprintf("SN-%s-%s", now.Format("20060102"), suffix)
	// 同一天同一台机器重复签发时加序号后缀，保持唯一。
	sn := base
	for i := 2; ; i++ {
		dup := false
		for _, e := range s.store.List(Query{Keyword: sn}) {
			if e.Serial == sn {
				dup = true
				break
			}
		}
		if !dup {
			return sn
		}
		sn = fmt.Sprintf("%s-%d", base, i)
		if i > 99 {
			return fmt.Sprintf("%s-%d", base, s.now().UnixNano())
		}
	}
}

// resolveFeatures 归一化功能列表：空表示全开，未知功能报错。
func resolveFeatures(in []license.Feature) ([]license.Feature, error) {
	if len(in) == 0 {
		return append([]license.Feature(nil), license.AllFeatures...), nil
	}
	seen := make(map[license.Feature]struct{}, len(in))
	out := make([]license.Feature, 0, len(in))
	for _, f := range in {
		f = license.Feature(strings.TrimSpace(string(f)))
		if f == "" {
			continue
		}
		if !license.IsKnownFeature(f) {
			return nil, fmt.Errorf("未知功能 %q，可选：kb,chat,ocr,office", f)
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, errors.New("至少需要指定一个功能")
	}
	return out, nil
}

// parseFeatures 解析逗号分隔的功能串。
func parseFeatures(raw string) ([]license.Feature, error) {
	// 分隔符必须包含 '+'：批量页的占位符与提示写的就是「功能用 + 连接
	// （kb+chat+ocr+office）」，少了它用户照着抄反而全行报「未知功能」。
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == '+' || r == ' '
	})
	out := make([]license.Feature, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, license.Feature(p))
	}
	return resolveFeatures(out)
}

// parseDays 解析「有效天数」列，兼容「365」「365天」「一年」这类写法。
func parseDays(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "天")
	s = strings.TrimSuffix(s, "日")
	if s == "" {
		return 0, errors.New("有效天数不能为空")
	}
	var days int
	if _, err := fmt.Sscanf(s, "%d", &days); err != nil {
		return 0, fmt.Errorf("有效天数不是整数：%q", raw)
	}
	if days < 0 {
		return 0, fmt.Errorf("有效天数不能为负：%q", raw)
	}
	return days, nil
}

func countNonEmpty(items []string) int {
	n := 0
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			n++
		}
	}
	return n
}

// normalizeMACForHeader 只做「这一串像不像 MAC」的形态判断，
// 不关心它是否可用（过滤本地管理地址是 license 包的职责）。
//
// 放在这里而不是复用 license 包的私有函数，是因为表头识别属于输入格式
// 判断：用户写在表头里的 `MAC` 本来就解析不成 MAC，用它就足够了。
func normalizeMACForHeader(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", false
	}
	for _, sep := range []string{":", "-", "."} {
		s = strings.ReplaceAll(s, sep, "")
	}
	if len(s) != 12 {
		return "", false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return "", false
		}
	}
	return s, true
}
