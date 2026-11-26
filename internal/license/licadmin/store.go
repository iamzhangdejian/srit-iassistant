// Package licadmin 是**发码管理端**的服务端实现：接收 MAC 地址，
// 用私钥签发授权码，并把每一张发出去的码记进台账。
//
// 它与客户端严格分离，这是整个授权体系的信任边界：
//
//   - 私钥只在这里。客户端只内置公钥，逆向客户端最多拿到公钥，
//     造不出新码。
//   - 它**不**打包进桌面端安装包。部署在只允许管理员访问的机器上。
//   - 它不做「在线激活」，因为桌面端是离线场景。发码端只负责签发和
//     记账，校验完全在客户端本地完成。
//
// 台账存在的意义不只是「查一下发过哪些码」：
//
//   - 客户打电话说「我的码不能用了」，客服要能立刻查到这张码绑的是
//     哪几块网卡、什么时候到期、是不是已经作废——没有台账就只能在
//     电话里靠用户回忆。
//   - 换机重签时要知道旧码是哪张，才能标记作废，避免一台机器两张有效
//     码各用各的。
//   - 出了安全事故要能回答「哪些码可能受影响」。
package licadmin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/license"
)

// Status 是台账里一张授权码的生命周期状态。
type Status string

const (
	// StatusActive 表示正常签发、尚未作废。
	StatusActive Status = "active"
	// StatusRevoked 表示已作废。
	//
	// 作废只在台账层面生效，客户端手里的码本身仍然签名有效——
	// 离线场景下没法远程吊销。作废的真实用途是「标记不再续期、
	// 不再提供技术支持」，以及避免换机后两张码同时在用导致客服
	// 分不清哪张是新的。这一点必须对使用者讲清楚，否则会产生
	// 「我点了作废怎么他还能用」的错觉。
	StatusRevoked Status = "revoked"
)

// Entry 是台账里的一条记录。
//
// 字段刻意保留原始信息（MAC 原样、授权码全文），而不是只存哈希：
// 授权码本身不含秘密（真正保密的是私钥），存全文才能支持「把这张码
// 再发给他一次」这个高频需求。
type Entry struct {
	// ID 是内部自增主键，用于后续作废/查询。
	ID int64 `json:"id"`
	// Serial 是授权码里的序列号，业务上的唯一标识。
	Serial string `json:"serial"`
	// Code 是完整授权码文本。
	Code string `json:"code"`
	// Fingerprint 是机器指纹。
	Fingerprint string `json:"fingerprint"`
	// MACs 是绑定的 MAC 地址（已归一化）。
	MACs []string `json:"macs"`
	// User 是使用人。
	User string `json:"user,omitempty"`
	// Org 是使用单位。
	Org string `json:"org,omitempty"`
	// Features 是功能位图。
	Features []license.Feature `json:"features"`
	// IssuedAt 是签发时间（Unix 秒）。
	IssuedAt int64 `json:"issued_at"`
	// ExpiresAt 是到期时间（Unix 秒），0 表示永久。
	ExpiresAt int64 `json:"expires_at"`
	// Status 是当前状态。
	Status Status `json:"status"`
	// Note 是运维备注，比如「2026-11 换机重签，旧码 SN-xxx」。
	Note string `json:"note,omitempty"`
	// RevokedAt 是作废时间（Unix 秒），0 表示未作废。
	RevokedAt int64 `json:"revoked_at,omitempty"`
	// ParentID 指向续期前的旧码（0 表示首次签发）。
	//
	// 续期不覆盖旧记录而是新签一张，是为了保留完整链路：客服问
	// 「这台机器换过几次码」时，顺着 ParentID 能一路追到最初那张。
	ParentID int64 `json:"parent_id,omitempty"`
	// KeyID 是签发这张码时使用的公钥指纹，用于事后核对「这张码是谁签的」。
	//
	// 换钥事故里，台账是唯一能重建时间线的地方：哪张码是旧钥签的、
	// 哪张是新钥签的、哪一天换的，都靠这一列分辨。老台账没有这个字段，
	// 空值表示签发者未知。
	KeyID string `json:"key_id,omitempty"`
	// RevokeReason 是作废原因（自动续期、换机、客户退订……）。
	//
	// 与 Note 分开的原因：Note 是人工备注，会越写越长；作废原因是
	// 结构化的、要用来统计的口径，混在一起就再也拆不出来了。
	RevokeReason string `json:"revoke_reason,omitempty"`
}

// ExpiresText 返回给人看的到期描述。
func (e Entry) ExpiresText() string {
	if e.ExpiresAt == 0 {
		return "永久"
	}
	return time.Unix(e.ExpiresAt, 0).Format("2006-01-02")
}

// RemainingDays 返回剩余天数；已过期返回负数。
//
// 与客户端 license.remainingDays 的差别在于它允许负数：台账是给管理员
// 看的，「过期了 30 天」比「剩 0 天」信息量大得多。
func (e Entry) RemainingDays(now time.Time) int {
	if e.ExpiresAt == 0 {
		return 0
	}
	d := time.Unix(e.ExpiresAt, 0).Sub(now)
	return int(d / (24 * time.Hour))
}

// Store 是台账存储。用单个 JSON 文件而不是 SQLite：
//
// 发码端是低频操作（一天几十张码就算多），一个 JSON 文件足够，而且
// 让备份与迁移变成「复制一个文件」——运维成本几乎为零。真正需要
// 关系查询时再换 SQLite 也不迟，接口是按 repository 的形状写的。
type Store struct {
	mu   sync.Mutex
	path string
	// nextID 是单调递增的主键。不用 len(entries)+1：删过记录后会撞号。
	nextID int64
	loaded bool
	byID   map[int64]*Entry
	// order 保留插入顺序，便于「最近的排在前面」展示。
	order []int64
}

// NewStore 创建台账存储。path 是 JSON 文件路径。
func NewStore(path string) *Store {
	return &Store{path: path, nextID: 1, byID: make(map[int64]*Entry)}
}

// loadLocked 从磁盘加载。调用方必须已持锁。
func (s *Store) loadLocked() error {
	if s.loaded {
		return nil
	}
	s.loaded = true
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // 首次启动，正常
		}
		return fmt.Errorf("licadmin: read ledger: %w", err)
	}
	var payload struct {
		NextID  int64    `json:"next_id"`
		Entries []*Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("licadmin: parse ledger: %w", err)
	}
	for _, e := range payload.Entries {
		if e == nil {
			continue
		}
		s.byID[e.ID] = e
		s.order = append(s.order, e.ID)
		if e.ID >= s.nextID {
			s.nextID = e.ID + 1
		}
	}
	if payload.NextID > s.nextID {
		s.nextID = payload.NextID
	}
	return nil
}

// saveLocked 原子落盘。调用方必须已持锁。
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("licadmin: create ledger dir: %w", err)
	}
	entries := make([]*Entry, 0, len(s.order))
	for _, id := range s.order {
		if e, ok := s.byID[id]; ok {
			entries = append(entries, e)
		}
	}
	data, err := json.MarshalIndent(struct {
		NextID  int64    `json:"next_id"`
		Entries []*Entry `json:"entries"`
	}{NextID: s.nextID, Entries: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("licadmin: encode ledger: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("licadmin: write ledger: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("licadmin: replace ledger: %w", err)
	}
	return nil
}

// Add 追加一条记录并分配 ID。返回带 ID 的副本。
func (s *Store) Add(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Entry{}, err
	}
	e.ID = s.nextID
	s.nextID++
	stored := e
	s.byID[e.ID] = &stored
	s.order = append(s.order, e.ID)
	if err := s.saveLocked(); err != nil {
		// 落盘失败时回滚内存，避免「内存里有、磁盘上没有」的假象。
		delete(s.byID, e.ID)
		s.order = s.order[:len(s.order)-1]
		s.nextID--
		return Entry{}, err
	}
	return stored, nil
}

// Get 按 ID 取一条。
func (s *Store) Get(id int64) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Entry{}, false
	}
	e, ok := s.byID[id]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Revoke 作废一条记录。已经作废的重复调用是幂等的。
func (s *Store) Revoke(id int64, note string, now time.Time) (Entry, error) {
	return s.RevokeWithReason(id, "", note, now)
}

// RevokeWithReason 作废一条记录并记录结构化原因。
func (s *Store) RevokeWithReason(id int64, reason, note string, now time.Time) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Entry{}, err
	}
	e, ok := s.byID[id]
	if !ok {
		return Entry{}, fmt.Errorf("licadmin: entry %d not found", id)
	}
	if e.Status != StatusRevoked {
		e.Status = StatusRevoked
		e.RevokedAt = now.Unix()
		if reason != "" {
			e.RevokeReason = reason
		}
		if note != "" {
			if e.Note != "" {
				e.Note += "；"
			}
			e.Note += note
		}
		if err := s.saveLocked(); err != nil {
			return Entry{}, err
		}
	}
	return *e, nil
}

// Query 是台账查询条件。
type Query struct {
	// Keyword 在序列号、使用人、单位、MAC 上做子串匹配。
	Keyword string
	// Status 过滤状态，空表示全部。
	Status Status
	// Limit 限制返回条数，0 表示不限。
	Limit int
	// Offset 跳过前 N 条，用于分页。
	Offset int
}

// List 按条件查询，最近的排在前面。
func (s *Store) List(q Query) []Entry {
	page, _ := s.ListPage(q)
	return page
}

// ListPage 按条件查询并同时返回命中总数，供页面分页显示。
//
// 单独返回总数是刻意的：页面要显示「共 1,284 条」，如果只返回当前页，
// 前端就得自己猜总数，或者再发一次不带 Limit 的请求——后者在台账变大
// 后会很慢。
func (s *Store) ListPage(q Query) ([]Entry, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return nil, 0
	}
	kw := strings.ToLower(strings.TrimSpace(q.Keyword))
	matched := 0
	skipped := 0
	out := make([]Entry, 0, len(s.order))
	// 从后往前遍历，天然得到「最新在前」。
	for i := len(s.order) - 1; i >= 0; i-- {
		e, ok := s.byID[s.order[i]]
		if !ok {
			continue
		}
		if q.Status != "" && e.Status != q.Status {
			continue
		}
		if kw != "" && !entryMatches(e, kw) {
			continue
		}
		matched++
		if q.Offset > 0 && skipped < q.Offset {
			skipped++
			continue
		}
		if q.Limit > 0 && len(out) >= q.Limit {
			continue // 继续数总数，但不再收集
		}
		out = append(out, *e)
	}
	return out, matched
}

// entryMatches 判断一条记录是否命中关键词。
func entryMatches(e *Entry, kw string) bool {
	if strings.Contains(strings.ToLower(e.Serial), kw) ||
		strings.Contains(strings.ToLower(e.User), kw) ||
		strings.Contains(strings.ToLower(e.Org), kw) ||
		strings.Contains(strings.ToLower(e.Note), kw) ||
		strings.Contains(strings.ToLower(e.Fingerprint), kw) {
		return true
	}
	for _, mac := range e.MACs {
		if strings.Contains(mac, kw) {
			return true
		}
	}
	return false
}

// FindActiveByMACs 查找与给定 MAC 有交集、且仍然有效的授权记录。
//
// 用途：签新码之前提示「这台机器已经有一张有效的码了」，避免同一台
// 机器上两张码并存——那种情况下用户不知道该填哪张，客服也很难判断。
func (s *Store) FindActiveByMACs(macs []string, now time.Time) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return nil
	}
	want := license.NormalizeMACs(macs)
	out := make([]Entry, 0, 2)
	for _, id := range s.order {
		e, ok := s.byID[id]
		if !ok || e.Status == StatusRevoked {
			continue
		}
		if e.ExpiresAt != 0 && now.After(time.Unix(e.ExpiresAt, 0)) {
			continue
		}
		if license.Matches(e.MACs, want) {
			out = append(out, *e)
		}
	}
	return out
}

// Stats 是台账概览，给管理页面顶部显示。
type Stats struct {
	Total int `json:"total"`
	// Active 是未作废且未过期的数量。
	Active int `json:"active"`
	// Expired 是未作废但已过期的数量。
	Expired int `json:"expired"`
	// ExpiringSoon 是 30 天内到期的数量，用于运营跟进续费。
	ExpiringSoon int `json:"expiring_soon"`
	// Revoked 是作废数量。
	Revoked int `json:"revoked"`
}

// Stats 统计台账概览。
func (s *Store) Stats(now time.Time) Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Stats{}
	}
	var st Stats
	soon := now.AddDate(0, 0, 30)
	for _, id := range s.order {
		e, ok := s.byID[id]
		if !ok {
			continue
		}
		st.Total++
		switch {
		case e.Status == StatusRevoked:
			st.Revoked++
		case e.ExpiresAt == 0:
			st.Active++
		case now.After(time.Unix(e.ExpiresAt, 0)):
			st.Expired++
		default:
			st.Active++
			if time.Unix(e.ExpiresAt, 0).Before(soon) {
				st.ExpiringSoon++
			}
		}
	}
	return st
}

// SortedMACs 返回排好序的 MAC 副本，便于稳定输出到 CSV。
func SortedMACs(macs []string) []string {
	out := append([]string(nil), macs...)
	sort.Strings(out)
	return out
}
