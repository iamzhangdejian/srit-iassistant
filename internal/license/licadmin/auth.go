package licadmin

// 账号与会话（阶段 2）。
//
// 为什么要加账号：发码端以前只有一把长随机口令，口令泄露就等于授权体系
// 失守，而且没有任何「谁在什么时候发了哪张码」的记录——客户对账、续期
// 争议时全靠人工回忆。这里补三件事：单账号密码登录、服务端会话、失败锁定。
//
// 为什么是单账号：这是内网单人使用的发码工具，多账号/角色/权限矩阵的运维
// 成本远大于收益；真要多人用，应该先解决「谁负责发码」这个组织问题，
// 而不是在工具里堆权限位。表结构用 `CHECK (id = 1)` 把这条约束写进库，
// 避免以后有人顺手加出第二个管理员却忘了配套的权限设计。
//
// 数据落 SQLite 而不是 JSON：账号、锁定状态、审计日志都是「频繁小写入 +
// 按时间查询」，JSON 全量重写的模型（台账 ledger.json）在这里会丢数据。
// 台账本身仍留在 ledger.json（阶段 1 已定稿），迁移放到后面阶段做，
// 避免一次改动同时动「签发改数据模型」和「认证」两件高风险的事。

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"

	_ "github.com/mattn/go-sqlite3"
)

const (
	// sessionCookieName 是会话 Cookie 名。
	sessionCookieName = "licadmin_session"
	// sessionTTL 是会话有效期。12 小时覆盖一个工作日，又不至于「开了就
	// 一直有效」；滑动续期在剩余不足一半时重签。
	sessionTTL = 12 * time.Hour
	// sessionRenewAfter 是滑动续期阈值：剩余寿命低于它就重签 Cookie。
	sessionRenewAfter = 6 * time.Hour

	// freeAttempts 是「连续错几次开始锁」。5 次是常见运维工具的取值：
	// 手抖输错密码很少连续 5 次，而暴力猜解在 5 次后就被拖慢。
	freeAttempts = 5
	// lockBase / lockMax 是锁定时长：第 5 次锁 60 秒，之后每次翻倍，
	// 上限 15 分钟。上限的存在是为了不让「忘记密码」变成永久锁死。
	lockBase = 60 * time.Second
	lockMax  = 15 * time.Minute

	// 密码策略：至少 10 位、同时含字母与数字。
	minPasswordLen = 10
)

// scrypt 参数：N=2^15 在普通机器上约 30~60ms，足够把在线爆破压到很低，
// 又不会让登录明显卡顿。
const (
	scryptN      = 1 << 15
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
	scryptSalt   = 16
)

var (
	// ErrAuthNotSetup 表示还没有管理员账号。
	ErrAuthNotSetup = errors.New("尚未初始化管理员账号")
	// ErrBadCredentials 表示账号或密码不正确。
	ErrBadCredentials = errors.New("账号或密码不正确")
	// ErrAuthExists 表示已经初始化过，不能重复初始化。
	ErrAuthExists = errors.New("管理员账号已存在")
)

// LockedError 表示账号处于锁定中。
type LockedError struct {
	Until  time.Time
	Remain time.Duration
}

func (e *LockedError) Error() string {
	sec := int(e.Remain.Seconds()) + 1
	return fmt.Sprintf("登录失败次数过多，请 %d 秒后再试", sec)
}

// BadCredentialsError 带上「还能试几次」，让用户知道不是密码记错了一次而已。
type BadCredentialsError struct {
	Remaining int
}

func (e *BadCredentialsError) Error() string {
	if e.Remaining > 0 {
		return fmt.Sprintf("账号或密码不正确，还可尝试 %d 次", e.Remaining)
	}
	return "账号或密码不正确"
}

// AuditEntry 是一条审计记录。
type AuditEntry struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
	Detail string `json:"detail,omitempty"`
	IP     string `json:"ip,omitempty"`
}

// AuthStore 是账号、会话密钥与审计的存储。
type AuthStore struct {
	db *sql.DB
	// dir 是数据库所在目录，技能包等附件按同一根目录落盘。
	dir string
}

// OpenAuthStore 打开（必要时创建）数据库。
func OpenAuthStore(path string) (*AuthStore, error) {
	// _busy_timeout 让并发写入等待而不是直接报 SQLITE_BUSY；
	// WAL 让「写审计」不会被「读台账」卡住。
	dsn := path + "?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 单文件，连接池大于 1 只会在写锁上互相等。
	db.SetMaxOpenConns(1)
	a := &AuthStore{db: db, dir: filepath.Dir(path)}
	if err := a.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return a, nil
}

// Close 关闭数据库。
// DB 暴露连接，供同目录的其他存储（技能库）复用同一把写锁。
func (a *AuthStore) DB() *sql.DB { return a.db }

// DataDir 返回数据目录。
func (a *AuthStore) DataDir() string { return a.dir }

func (a *AuthStore) Close() error {
	if a == nil || a.db == nil {
		return nil
	}
	return a.db.Close()
}

// migrate 建表。用 CREATE TABLE IF NOT EXISTS 而不是迁移框架：
// 这里是单机工具，schema 只有三张表，加一版迁移工具反而更难维护。
func (a *AuthStore) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS admin_user (
			id              INTEGER PRIMARY KEY CHECK (id = 1),
			username        TEXT    NOT NULL,
			password_salt   BLOB    NOT NULL,
			password_hash   BLOB    NOT NULL,
			session_key     BLOB    NOT NULL,
			failed_attempts INTEGER NOT NULL DEFAULT 0,
			locked_until    INTEGER NOT NULL DEFAULT 0,
			last_login_at   INTEGER NOT NULL DEFAULT 0,
			created_at      INTEGER NOT NULL,
			updated_at      INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit (
			id     INTEGER PRIMARY KEY AUTOINCREMENT,
			ts     INTEGER NOT NULL,
			actor  TEXT    NOT NULL,
			action TEXT    NOT NULL,
			target TEXT    NOT NULL DEFAULT '',
			detail TEXT    NOT NULL DEFAULT '',
			ip     TEXT    NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit (ts DESC)`,
	}
	for _, s := range stmts {
		if _, err := a.db.Exec(s); err != nil {
			return fmt.Errorf("初始化数据库失败：%w", err)
		}
	}
	return nil
}

// SetupRequired 报告是否还需要初始化管理员。
func (a *AuthStore) SetupRequired(ctx context.Context) (bool, error) {
	var n int
	if err := a.db.QueryRowContext(ctx, `SELECT count(*) FROM admin_user`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// ValidatePassword 检查密码策略。
//
// 不引入「必须含符号」这一条：用户会把密码写在便签上，越复杂的规则越容易
// 变成「贴在显示器上」。长度 + 字母 + 数字就能挡住绝大多数在线猜测，而
// 真正的防线是失败锁定与只监听本机。
func ValidatePassword(username, password string) error {
	if len([]rune(password)) < minPasswordLen {
		return fmt.Errorf("密码至少 %d 位", minPasswordLen)
	}
	if strings.EqualFold(strings.TrimSpace(password), strings.TrimSpace(username)) {
		return errors.New("密码不能与账号相同")
	}
	var hasLetter, hasDigit bool
	for _, r := range password {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return errors.New("密码要同时包含字母和数字")
	}
	return nil
}

// hashPassword 用 scrypt 派生哈希。
//
// 为什么不用 bcrypt：scrypt 对内存的硬性要求让 GPU/ASIC 并行猜解更贵，
// 而 golang.org/x/crypto/scrypt 已经在依赖里（不需要新增模块）。
func hashPassword(password string) (salt, hash []byte, err error) {
	salt = make([]byte, scryptSalt)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, err
	}
	hash, err = scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, nil, err
	}
	return salt, hash, nil
}

// verifyPassword 以常量时间比较哈希。
func verifyPassword(password string, salt, want []byte) bool {
	got, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// CreateAdmin 初始化管理员账号。已存在时返回 ErrAuthExists。
func (a *AuthStore) CreateAdmin(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("请填写账号")
	}
	if err := ValidatePassword(username, password); err != nil {
		return err
	}
	salt, hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	now := time.Now()
	_, err = a.db.ExecContext(ctx,
		`INSERT INTO admin_user (id, username, password_salt, password_hash, session_key,
			failed_attempts, locked_until, last_login_at, created_at, updated_at)
		 VALUES (1, ?, ?, ?, ?, 0, 0, 0, ?, ?)`,
		username, salt, hash, key, now.Unix(), now.Unix())
	if err != nil {
		// 主键冲突 = 已经初始化过；换成人能看懂的错误。
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "constraint") {
			return ErrAuthExists
		}
		return err
	}
	return nil
}

// Authenticate 校验账号密码，并维护失败计数与锁定。
//
// 返回的 error 可能是 *LockedError、*BadCredentialsError、ErrAuthNotSetup。
// 调用方要靠类型分支决定 HTTP 状态码（423 / 401 / 409），所以这里不用
// 字符串匹配来判断。
func (a *AuthStore) Authenticate(ctx context.Context, username, password string, now time.Time) (string, error) {
	var (
		dbUser   string
		salt     []byte
		hash     []byte
		attempts int
		locked   int64
	)
	err := a.db.QueryRowContext(ctx,
		`SELECT username, password_salt, password_hash, failed_attempts, locked_until
		 FROM admin_user WHERE id = 1`).Scan(&dbUser, &salt, &hash, &attempts, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAuthNotSetup
	}
	if err != nil {
		return "", err
	}

	if locked > 0 {
		until := time.Unix(locked, 0)
		if until.After(now) {
			return "", &LockedError{Until: until, Remain: until.Sub(now)}
		}
		// 锁定过期后**不清零**计数：连续失败要跨锁定累计，否则每次解锁
		// 都从「还差 5 次」重新开始，暴力猜解只要等 1 分钟就能无限试。
		// 计数只在登录成功时清零。
	}

	ok := verifyPassword(password, salt, hash) && strings.EqualFold(strings.TrimSpace(username), dbUser)
	if ok {
		_, err := a.db.ExecContext(ctx,
			`UPDATE admin_user SET failed_attempts = 0, locked_until = 0, last_login_at = ?, updated_at = ? WHERE id = 1`,
			now.Unix(), now.Unix())
		if err != nil {
			return "", err
		}
		return dbUser, nil
	}

	attempts++
	var lockUntil int64
	if attempts >= freeAttempts {
		// 第 5 次：60 秒；第 6 次：120 秒；…… 上限 15 分钟。
		d := lockBase
		for i := freeAttempts; i < attempts; i++ {
			d *= 2
			if d >= lockMax {
				d = lockMax
				break
			}
		}
		if d > lockMax {
			d = lockMax
		}
		lockUntil = now.Add(d).Unix()
	}
	if _, err := a.db.ExecContext(ctx,
		`UPDATE admin_user SET failed_attempts = ?, locked_until = ?, updated_at = ? WHERE id = 1`,
		attempts, lockUntil, now.Unix()); err != nil {
		return "", err
	}
	if lockUntil > 0 {
		return "", &LockedError{Until: time.Unix(lockUntil, 0), Remain: time.Until(time.Unix(lockUntil, 0))}
	}
	return "", &BadCredentialsError{Remaining: freeAttempts - attempts}
}

// ChangePassword 校验旧密码后更新，并轮换会话密钥。
//
// 轮换 session_key 是「改密即踢掉所有其他会话」的实现方式：会话令牌是
// 无状态 HMAC，不轮换密钥就无法让已签发的 Cookie 失效，而给会话建表又
// 会把单机工具做成数据库应用。轮换的代价只是让当前会话重签一次。
func (a *AuthStore) ChangePassword(ctx context.Context, username, current, next string, now time.Time) error {
	var (
		dbUser string
		salt   []byte
		hash   []byte
	)
	err := a.db.QueryRowContext(ctx,
		`SELECT username, password_salt, password_hash FROM admin_user WHERE id = 1`).
		Scan(&dbUser, &salt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAuthNotSetup
	}
	if err != nil {
		return err
	}
	if !verifyPassword(current, salt, hash) {
		return errors.New("当前密码不正确")
	}
	if err := ValidatePassword(dbUser, next); err != nil {
		return err
	}
	newSalt, newHash, err := hashPassword(next)
	if err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx,
		`UPDATE admin_user SET password_salt = ?, password_hash = ?, session_key = ?, updated_at = ? WHERE id = 1`,
		newSalt, newHash, key, now.Unix())
	return err
}

// SessionKey 返回当前会话签名密钥。
func (a *AuthStore) SessionKey(ctx context.Context) ([]byte, error) {
	var key []byte
	err := a.db.QueryRowContext(ctx, `SELECT session_key FROM admin_user WHERE id = 1`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAuthNotSetup
	}
	return key, err
}

// Username 返回管理员账号（用于界面显示）。
func (a *AuthStore) Username(ctx context.Context) (string, error) {
	var name string
	err := a.db.QueryRowContext(ctx, `SELECT username FROM admin_user WHERE id = 1`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAuthNotSetup
	}
	return name, err
}

// AppendAudit 写一条审计。
//
// 审计写失败不影响主流程：发码成功但审计失败时，宁可少一条日志也不能
// 让用户以为「码没发出去」而重复签发。
func (a *AuthStore) AppendAudit(ctx context.Context, e AuditEntry) {
	if a == nil {
		return
	}
	ts := e.TS
	if ts == 0 {
		ts = time.Now().Unix()
	}
	_, _ = a.db.ExecContext(ctx,
		`INSERT INTO audit (ts, actor, action, target, detail, ip) VALUES (?, ?, ?, ?, ?, ?)`,
		ts, e.Actor, e.Action, e.Target, e.Detail, e.IP)
}

// ListAudit 返回最近的审计记录。
func (a *AuthStore) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := a.db.QueryContext(ctx,
		`SELECT id, ts, actor, action, target, detail, ip FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AuditEntry, 0, limit)
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Target, &e.Detail, &e.IP); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- 会话令牌 ----------
//
// 令牌格式：base64url(payload).base64url(HMAC-SHA256(key, payload))
// payload = {"uid":1,"exp":<unix>}
//
// 不用 JWT：这里只有三个字段、不需要跨服务互认，引入 JWT 库反而多一处
// 需要审计的第三方代码（"alg":"none" 这类历史事故都来自这些库）。

type sessionPayload struct {
	UID int64 `json:"uid"`
	Exp int64 `json:"exp"`
}

// SignSession 生成会话令牌。
func SignSession(key []byte, uid int64, exp time.Time) string {
	payload, _ := json.Marshal(sessionPayload{UID: uid, Exp: exp.Unix()})
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ParseSession 校验令牌并返回过期时间。
func ParseSession(key []byte, token string, now time.Time) (time.Time, bool) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return time.Time{}, false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return time.Time{}, false
	}
	var p sessionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return time.Time{}, false
	}
	exp := time.Unix(p.Exp, 0)
	if !exp.After(now) {
		return time.Time{}, false
	}
	return exp, true
}
