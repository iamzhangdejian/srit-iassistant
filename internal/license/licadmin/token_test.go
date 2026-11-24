package licadmin

import (
	"strings"
	"testing"
	"time"
)

func newTokenTestStore(t *testing.T) *TokenStore {
	t.Helper()
	db := openTestDB(t)
	ts, err := NewTokenStore(db)
	if err != nil {
		t.Fatalf("建令牌库失败：%v", err)
	}
	return ts
}

func TestTokenCreateStoresOnlyHash(t *testing.T) {
	ts := newTokenTestStore(t)
	now := time.Now()

	tok, plain, err := ts.Create("CI 发码", []string{ScopeLicenses}, now)
	if err != nil {
		t.Fatalf("创建失败：%v", err)
	}
	if !strings.HasPrefix(plain, "gyt_") {
		t.Fatalf("明文应有 gyt_ 前缀，实际 %q", plain)
	}
	if len(tok.Prefix) != 12 || !strings.HasPrefix(plain, tok.Prefix) {
		t.Fatalf("prefix 应为明文前 12 位，实际 %q", tok.Prefix)
	}

	// 库里不能出现明文：这是「泄漏后仍安全」的全部依据
	var hash string
	if err := ts.db.QueryRow(`SELECT hash FROM api_token WHERE id = ?`, tok.ID).Scan(&hash); err != nil {
		t.Fatalf("读 hash 失败：%v", err)
	}
	if strings.Contains(hash, plain) || len(hash) != 64 {
		t.Fatalf("库里应只有 64 位十六进制哈希，实际 %q", hash)
	}
}

func TestTokenLookupRequiresScope(t *testing.T) {
	ts := newTokenTestStore(t)
	now := time.Now()
	_, plain, err := ts.Create("只读技能", []string{ScopeSkills}, now)
	if err != nil {
		t.Fatalf("创建失败：%v", err)
	}

	tok, err := ts.Lookup(plain, now)
	if err != nil {
		t.Fatalf("应当能查到自己：%v", err)
	}
	if !tok.HasScope(ScopeSkills) {
		t.Fatal("应当有 skills 用途")
	}
	if tok.HasScope(ScopeLicenses) {
		t.Fatal("不该有 licenses 用途")
	}

	if _, err := ts.Lookup("gyt_deadbeef", now); err == nil {
		t.Fatal("错误令牌应查不到")
	}
}

func TestTokenRevokeStopsLookup(t *testing.T) {
	ts := newTokenTestStore(t)
	now := time.Now()
	tok, plain, err := ts.Create("临时", []string{ScopeLicenses, ScopeReleases}, now)
	if err != nil {
		t.Fatalf("创建失败：%v", err)
	}
	if err := ts.Revoke(tok.ID, now); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, err := ts.Lookup(plain, now); err == nil {
		t.Fatal("撤销后不该再能用")
	}
	if err := ts.Revoke(tok.ID, now); err != nil {
		t.Fatalf("重复撤销应幂等：%v", err)
	}
	if err := ts.Revoke(9999, now); err == nil {
		t.Fatal("撤销不存在的令牌应报错")
	}
}

func TestTokenCreateRejectsEmptyLabelAndScopes(t *testing.T) {
	ts := newTokenTestStore(t)
	now := time.Now()
	if _, _, err := ts.Create("  ", []string{ScopeLicenses}, now); err == nil {
		t.Fatal("标签为空应拒绝")
	}
	if _, _, err := ts.Create("x", nil, now); err == nil {
		t.Fatal("没有用途应拒绝——签出「什么都不给」的令牌只会让集成方以为坏了")
	}
	if _, _, err := ts.Create("x", []string{"unknown"}, now); err == nil {
		t.Fatal("未知用途应拒绝")
	}
	// 重复用途去重后仍可用
	tok, _, err := ts.Create("y", []string{ScopeLicenses, ScopeLicenses}, now)
	if err != nil {
		t.Fatalf("重复用途应去重而不是报错：%v", err)
	}
	if len(tok.Scopes) != 1 {
		t.Fatalf("去重后应只剩 1 个用途，实际 %v", tok.Scopes)
	}
}

func TestTokenListHidesRevoked(t *testing.T) {
	ts := newTokenTestStore(t)
	now := time.Now()
	tok, _, err := ts.Create("甲", []string{ScopeLicenses}, now)
	if err != nil {
		t.Fatalf("创建失败：%v", err)
	}
	if _, _, err := ts.Create("乙", []string{ScopeSkills}, now); err != nil {
		t.Fatalf("创建失败：%v", err)
	}
	if err := ts.Revoke(tok.ID, now); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	list, err := ts.List()
	if err != nil {
		t.Fatalf("列表失败：%v", err)
	}
	if len(list) != 1 || list[0].Label != "乙" {
		t.Fatalf("列表应只剩未撤销的一条，实际 %+v", list)
	}
}
