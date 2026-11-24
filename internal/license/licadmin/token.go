package licadmin

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 自动化令牌（阶段 4：按用途签发）。
//
// 阶段 2 只有一把静态令牌（`-token`），能做到「能发码不能改密码」，但做不到
// 「给 CI 发码的那把不能推技能/版本」。实际部署里这两件事往往由不同的人/流水线
// 负责，一把钥匙通吃就意味着任何一处泄漏都要把所有集成同时改一遍。
//
// 所以这里把令牌做成「一把令牌一组用途（scope）」：
//   - licenses：签发/续期/作废/台账/导出/验签（流水线脚本最常用）
//   - skills  ：读技能库、校验、导出技能包（构建机拉包用）
//   - releases：读发布记录、下载发行包（发布流水线用）
//
// 上传/发布/删除三类写操作一律只认会话，令牌再怎么配都不给——它们决定
// 客户机器上装什么，写权限应该留在人手里。
//
// 令牌在库里只存 SHA-256，明文只在创建响应里出现一次；泄漏后无法从库里还原。

// 令牌用途。
const (
	ScopeLicenses = "licenses"
	ScopeSkills   = "skills"
	ScopeReleases = "releases"
)

// allTokenScopes 是允许签发用途的集合，顺序即界面展示顺序。
var allTokenScopes = []string{ScopeLicenses, ScopeSkills, ScopeReleases}

var (
	// ErrTokenNotFound 表示令牌不存在或已撤销。
	ErrTokenNotFound = errors.New("令牌不存在或已撤销")
	// ErrTokenScopeInvalid 表示请求了不支持的用途。
	ErrTokenScopeInvalid = errors.New("令牌用途不合法")
	// ErrTokenLabelRequired 表示给令牌起名是必填项——没有名字的令牌
	// 在泄漏时根本不知道该撤销哪一把。
	ErrTokenLabelRequired = errors.New("请给令牌起个名字")
)

// APIToken 是令牌的元信息（不含明文）。
type APIToken struct {
	ID         int64    `json:"id"`
	Label      string   `json:"label"`
	Scopes     []string `json:"scopes"`
	Prefix     string   `json:"prefix"`
	CreatedAt  int64    `json:"created_at"`
	LastUsedAt int64    `json:"last_used_at"`
	RevokedAt  int64    `json:"revoked_at"`
}

// TokenStore 管理按用途签发的令牌。
type TokenStore struct {
	db *sql.DB
}

// NewTokenStore 在既有数据库上初始化令牌表。
func NewTokenStore(db *sql.DB) (*TokenStore, error) {
	if db == nil {
		return nil, errors.New("令牌库未初始化")
	}
	s := &TokenStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *TokenStore) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS api_token (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	label        TEXT    NOT NULL,
	hash         TEXT    NOT NULL UNIQUE,
	prefix       TEXT    NOT NULL,
	scopes       TEXT    NOT NULL,
	created_at   INTEGER NOT NULL,
	last_used_at INTEGER NOT NULL DEFAULT 0,
	revoked_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_api_token_revoked ON api_token(revoked_at);
`)
	return err
}

// normalizeScopes 去重、排序并按固定顺序输出，同时拒绝未知用途。
func normalizeScopes(scopes []string) ([]string, error) {
	seen := make(map[string]bool, len(scopes))
	for _, sc := range scopes {
		sc = strings.TrimSpace(strings.ToLower(sc))
		if sc == "" {
			continue
		}
		known := false
		for _, allowed := range allTokenScopes {
			if sc == allowed {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("%w：%s", ErrTokenScopeInvalid, sc)
		}
		seen[sc] = true
	}
	if len(seen) == 0 {
		// 空用途等于「不给任何能力」，签出来只会让集成方以为坏了，
		// 所以直接拒绝并提示用途清单。
		return nil, fmt.Errorf("%w：至少选择一个用途（%s）",
			ErrTokenScopeInvalid, strings.Join(allTokenScopes, " / "))
	}
	out := make([]string, 0, len(seen))
	for _, sc := range allTokenScopes {
		if seen[sc] {
			out = append(out, sc)
		}
	}
	return out, nil
}

// hashToken 只存哈希：库里拿不到明文，备份泄漏也不至于直接能用。
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create 生成一把新令牌，返回元信息与明文（明文只出现这一次）。
func (s *TokenStore) Create(label string, scopes []string, now time.Time) (APIToken, string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return APIToken{}, "", ErrTokenLabelRequired
	}
	norm, err := normalizeScopes(scopes)
	if err != nil {
		return APIToken{}, "", err
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return APIToken{}, "", err
	}
	plain := "gyt_" + hex.EncodeToString(raw)
	prefix := plain[:12] // gyt_xxxxxxxx，界面用它区分是哪一把
	hash := hashToken(plain)

	res, err := s.db.Exec(
		`INSERT INTO api_token (label, hash, prefix, scopes, created_at) VALUES (?, ?, ?, ?, ?)`,
		label, hash, prefix, strings.Join(norm, ","), now.Unix(),
	)
	if err != nil {
		return APIToken{}, "", err
	}
	id, _ := res.LastInsertId()
	return APIToken{
		ID:        id,
		Label:     label,
		Scopes:    norm,
		Prefix:    prefix,
		CreatedAt: now.Unix(),
	}, plain, nil
}

// List 返回所有未撤销的令牌（最新的在前）。
func (s *TokenStore) List() ([]APIToken, error) {
	rows, err := s.db.Query(
		`SELECT id, label, prefix, scopes, created_at, last_used_at, revoked_at
		   FROM api_token WHERE revoked_at = 0 ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []APIToken
	for rows.Next() {
		var t APIToken
		var scopes string
		if err := rows.Scan(&t.ID, &t.Label, &t.Prefix, &scopes, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		t.Scopes = splitScopes(scopes)
		out = append(out, t)
	}
	return out, rows.Err()
}

func splitScopes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// Lookup 用明文令牌换元信息；同时刷新 last_used_at 便于排查「哪把还在用」。
func (s *TokenStore) Lookup(token string, now time.Time) (APIToken, error) {
	hash := hashToken(token)
	row := s.db.QueryRow(
		`SELECT id, label, prefix, scopes, created_at, last_used_at, revoked_at
		   FROM api_token WHERE hash = ? AND revoked_at = 0`, hash)

	var t APIToken
	var scopes string
	if err := row.Scan(&t.ID, &t.Label, &t.Prefix, &scopes, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIToken{}, ErrTokenNotFound
		}
		return APIToken{}, err
	}
	t.Scopes = splitScopes(scopes)

	// last_used_at 每次请求都写会放大写入量；同一分钟内不重复写。
	if now.Unix()-t.LastUsedAt > 60 {
		if _, err := s.db.Exec(`UPDATE api_token SET last_used_at = ? WHERE id = ?`, now.Unix(), t.ID); err == nil {
			t.LastUsedAt = now.Unix()
		}
	}
	return t, nil
}

// Revoke 撤销令牌（软删，保留记录便于审计对账）。
// Revoke 撤销令牌；对「已撤销」的令牌幂等成功。
//
// 幂等的理由：界面上的撤销按钮可能被连点，脚本重跑也会重复调用，
// 第二次返回「不存在」只会让人误以为撤销失败；只有真的查无此 ID 才报错。
func (s *TokenStore) Revoke(id int64, now time.Time) error {
	res, err := s.db.Exec(
		`UPDATE api_token SET revoked_at = ? WHERE id = ? AND revoked_at = 0`, now.Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM api_token WHERE id = ?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrTokenNotFound
	}
	return nil
}

// HasScope 判断令牌是否具备某个用途。
func (t APIToken) HasScope(scope string) bool {
	for _, sc := range t.Scopes {
		if sc == scope {
			return true
		}
	}
	return false
}
