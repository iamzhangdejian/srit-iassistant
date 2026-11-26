package licadmin

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 版本发布（阶段 4）。
//
// 存在的理由：交付形态是离线安装包，客户现场没有 GitHub，也没有人会去
// 「打开网页看看有没有新版」。所以发布包与版本信息必须由管理端自己托管，
// 客户端只认一个 `WK_UPDATE_BASE_URL`。
//
// 三种角色看到的语义不同：
//   - 管理端：上传 → 草稿 → 发布（同一平台/架构/通道只保留一个已发布版本）
//   - 客户端：只问「有没有比我新的」，拿到下载地址与 sha256
//   - 运维：撤回或删除，改动立刻对客户端生效（下次检查更新时）
const (
	releaseStatusDraft     = "draft"
	releaseStatusPublished = "published"

	maxReleaseBytes = 2 << 30 // 2 GB：安装包本身就有 300~800 MB

	releaseChannelStable = "stable"
	releaseChannelBeta   = "beta"
)

// Release 是一条发布记录。Version 建议写 "0.9.0" 形态（不带 v），
// 比较时两种都接受。
type Release struct {
	ID          int64  `json:"id"`
	Platform    string `json:"platform"` // darwin / windows / linux
	Arch        string `json:"arch"`     // arm64 / amd64 / universal
	Channel     string `json:"channel"`  // stable / beta
	Version     string `json:"version"`
	Notes       string `json:"notes"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Status      string `json:"status"`
	UploadedAt  int64  `json:"uploaded_at"`
	PublishedAt int64  `json:"published_at"`
}

// ReleaseStore 管理发布包与版本元数据。文件落在数据目录下，
// 不塞进 SQLite：安装包动辄几百 MB，BLOB 会让备份和 VACUUM 都变慢。
type ReleaseStore struct {
	db      *sql.DB
	dataDir string
}

// NewReleaseStoreForData 建立发布库（与账号/技能共用同一个 licadmin.db）。
func NewReleaseStoreForData(db *sql.DB, dataDir string) (*ReleaseStore, error) {
	if db == nil {
		return nil, errors.New("发布库需要数据库连接")
	}
	if dataDir == "" {
		return nil, errors.New("发布库需要数据目录")
	}
	s := &ReleaseStore{db: db, dataDir: dataDir}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ReleaseStore) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS release (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    platform     TEXT NOT NULL,
    arch         TEXT NOT NULL,
    channel      TEXT NOT NULL DEFAULT 'stable',
    version      TEXT NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    filename     TEXT NOT NULL,
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    sha256       TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'draft',
    uploaded_at  INTEGER NOT NULL DEFAULT 0,
    published_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE (platform, arch, channel, version)
);
CREATE INDEX IF NOT EXISTS idx_release_status ON release (status);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("建发布表失败: %w", err)
	}
	return nil
}

// Dir 返回某条记录在磁盘上的目录（便于测试与排查）。
func (s *ReleaseStore) Dir(platform, arch, channel string) string {
	return filepath.Join(s.dataDir, "releases", platform, arch, channel)
}

// List 返回全部发布记录，按时间倒序。管理端要看到草稿，所以不带状态过滤。
func (s *ReleaseStore) List() ([]Release, error) {
	rows, err := s.db.Query(`SELECT id, platform, arch, channel, version, notes, filename,
        size_bytes, sha256, status, uploaded_at, published_at FROM release
        ORDER BY uploaded_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询发布记录失败: %w", err)
	}
	defer rows.Close()

	out := make([]Release, 0, 16)
	for rows.Next() {
		var r Release
		if err := rows.Scan(&r.ID, &r.Platform, &r.Arch, &r.Channel, &r.Version, &r.Notes,
			&r.Filename, &r.SizeBytes, &r.SHA256, &r.Status, &r.UploadedAt, &r.PublishedAt); err != nil {
			return nil, fmt.Errorf("读取发布记录失败: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get 按 id 取一条记录。
func (s *ReleaseStore) Get(id int64) (Release, error) {
	var r Release
	err := s.db.QueryRow(`SELECT id, platform, arch, channel, version, notes, filename,
        size_bytes, sha256, status, uploaded_at, published_at FROM release WHERE id = ?`, id).
		Scan(&r.ID, &r.Platform, &r.Arch, &r.Channel, &r.Version, &r.Notes,
			&r.Filename, &r.SizeBytes, &r.SHA256, &r.Status, &r.UploadedAt, &r.PublishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrReleaseNotFound
	}
	if err != nil {
		return r, fmt.Errorf("读取发布记录失败: %w", err)
	}
	return r, nil
}

var (
	// ErrReleaseNotFound 表示记录不存在（已删除或 id 写错）。
	ErrReleaseNotFound = errors.New("发布记录不存在")
	// ErrReleaseExists 表示同一平台/架构/通道/版本已经有记录。
	ErrReleaseExists = errors.New("该版本已存在")
	// ErrReleasePublished 表示记录已发布，不能直接删除。
	ErrReleasePublished = errors.New("已发布的版本不能删除，请先撤回")
)

// Import 保存上传的发布包并写入草稿记录。
//
// fingerprint 校验放在客户端下载后做，服务端这里只记录 sha256，
// 不重复校验内容——大文件算两遍 sha 会让上传慢一倍。
func (s *ReleaseStore) Import(r Release, data []byte, now time.Time) (Release, error) {
	r.Platform = strings.TrimSpace(strings.ToLower(r.Platform))
	r.Arch = strings.TrimSpace(strings.ToLower(r.Arch))
	r.Channel = strings.TrimSpace(strings.ToLower(r.Channel))
	if r.Channel == "" {
		r.Channel = releaseChannelStable
	}
	r.Version = normalizeVersion(r.Version)
	r.Filename = filepath.Base(strings.TrimSpace(r.Filename))

	if err := validateRelease(r); err != nil {
		return Release{}, err
	}
	if len(data) == 0 {
		return Release{}, errors.New("发布包是空的")
	}
	if int64(len(data)) > maxReleaseBytes {
		return Release{}, fmt.Errorf("发布包超过 %d MB 上限", maxReleaseBytes>>20)
	}

	dir := s.Dir(r.Platform, r.Arch, r.Channel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Release{}, fmt.Errorf("创建发布目录失败: %w", err)
	}
	path := filepath.Join(dir, r.Filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Release{}, fmt.Errorf("写入发布包失败: %w", err)
	}

	sum := sha256.Sum256(data)
	r.SHA256 = hex.EncodeToString(sum[:])
	r.SizeBytes = int64(len(data))
	r.Status = releaseStatusDraft
	r.UploadedAt = now.Unix()
	r.PublishedAt = 0

	res, err := s.db.Exec(`INSERT INTO release
        (platform, arch, channel, version, notes, filename, size_bytes, sha256, status, uploaded_at, published_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.Platform, r.Arch, r.Channel, r.Version, r.Notes, r.Filename, r.SizeBytes, r.SHA256,
		r.Status, r.UploadedAt, r.PublishedAt)
	if err != nil {
		// 唯一键冲突时把落盘的文件删掉，避免留下一条没人认领的包
		_ = os.Remove(path)
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Release{}, ErrReleaseExists
		}
		return Release{}, fmt.Errorf("写入发布记录失败: %w", err)
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

func validateRelease(r Release) error {
	switch r.Platform {
	case "darwin", "windows", "linux":
	default:
		return fmt.Errorf("平台只能是 darwin / windows / linux，收到 %q", r.Platform)
	}
	switch r.Arch {
	case "arm64", "amd64", "universal":
	default:
		return fmt.Errorf("架构只能是 arm64 / amd64 / universal，收到 %q", r.Arch)
	}
	switch r.Channel {
	case releaseChannelStable, releaseChannelBeta:
	default:
		return fmt.Errorf("通道只能是 stable / beta，收到 %q", r.Channel)
	}
	if r.Version == "" {
		return errors.New("版本号不能为空")
	}
	if _, ok := parseVersion(r.Version); !ok {
		return fmt.Errorf("版本号 %q 不是合法版本（形如 0.9.0）", r.Version)
	}
	if r.Filename == "" || r.Filename == "." || r.Filename == string(filepath.Separator) {
		return errors.New("发布包文件名不合法")
	}
	return nil
}

// Publish 发布某条记录：同一平台/架构/通道的旧版本退回草稿。
//
// 理由：客户端只会拿「最新的已发布版本」，同时留两个已发布版本只会让
// 排障时说不清到底谁在生效。
func (s *ReleaseStore) Publish(id int64, now time.Time) (Release, error) {
	r, err := s.Get(id)
	if err != nil {
		return r, err
	}
	if r.Status == releaseStatusPublished {
		return r, nil
	}
	if _, err := os.Stat(filepath.Join(s.Dir(r.Platform, r.Arch, r.Channel), r.Filename)); err != nil {
		return r, fmt.Errorf("发布包文件缺失（%s），请重新上传: %w", r.Filename, err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return r, fmt.Errorf("开始事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`UPDATE release SET status = ?, published_at = 0
        WHERE platform = ? AND arch = ? AND channel = ? AND status = ? AND id <> ?`,
		releaseStatusDraft, r.Platform, r.Arch, r.Channel, releaseStatusPublished, id); err != nil {
		return r, fmt.Errorf("下架旧版本失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE release SET status = ?, published_at = ? WHERE id = ?`,
		releaseStatusPublished, now.Unix(), id); err != nil {
		return r, fmt.Errorf("发布失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return r, fmt.Errorf("提交事务失败: %w", err)
	}
	return s.Get(id)
}

// Unpublish 撤回发布（客户端下次检查更新就看不到它了）。
func (s *ReleaseStore) Unpublish(id int64) (Release, error) {
	r, err := s.Get(id)
	if err != nil {
		return r, err
	}
	if _, err := s.db.Exec(`UPDATE release SET status = ?, published_at = 0 WHERE id = ?`,
		releaseStatusDraft, id); err != nil {
		return r, fmt.Errorf("撤回失败: %w", err)
	}
	return s.Get(id)
}

// Delete 删除草稿记录与磁盘文件；已发布的必须先撤回。
func (s *ReleaseStore) Delete(id int64) error {
	r, err := s.Get(id)
	if err != nil {
		return err
	}
	if r.Status == releaseStatusPublished {
		return ErrReleasePublished
	}
	if _, err := s.db.Exec(`DELETE FROM release WHERE id = ?`, id); err != nil {
		return fmt.Errorf("删除发布记录失败: %w", err)
	}
	_ = os.Remove(filepath.Join(s.Dir(r.Platform, r.Arch, r.Channel), r.Filename))
	return nil
}

// Path 返回某条记录对应的发布包绝对路径。
func (s *ReleaseStore) Path(r Release) string {
	return filepath.Join(s.Dir(r.Platform, r.Arch, r.Channel), r.Filename)
}

// Latest 返回某平台/架构/通道下版本最高的已发布记录。
//
// 找不到时返回 ErrReleaseNotFound（客户端据此显示「已是最新」以外的
// 「未配置更新源」提示，两者语义不同，不能都返回空记录）。
func (s *ReleaseStore) Latest(platform, arch, channel string) (Release, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	arch = strings.ToLower(strings.TrimSpace(arch))
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		channel = releaseChannelStable
	}

	rows, err := s.db.Query(`SELECT id, platform, arch, channel, version, notes, filename,
        size_bytes, sha256, status, uploaded_at, published_at FROM release
        WHERE platform = ? AND arch = ? AND channel = ? AND status = ?`,
		platform, arch, channel, releaseStatusPublished)
	if err != nil {
		return Release{}, fmt.Errorf("查询最新版本失败: %w", err)
	}
	defer rows.Close()

	best := Release{}
	found := false
	for rows.Next() {
		var r Release
		if err := rows.Scan(&r.ID, &r.Platform, &r.Arch, &r.Channel, &r.Version, &r.Notes,
			&r.Filename, &r.SizeBytes, &r.SHA256, &r.Status, &r.UploadedAt, &r.PublishedAt); err != nil {
			return Release{}, fmt.Errorf("读取发布记录失败: %w", err)
		}
		if !found || compareVersion(r.Version, best.Version) > 0 {
			best = r
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return Release{}, err
	}
	if !found {
		return Release{}, ErrReleaseNotFound
	}
	return best, nil
}

// ---- 版本号 ----

// normalizeVersion 去掉前缀 v、去掉首尾空白。存储统一不带 v，
// 展示与比较时都能兼容用户习惯的 "v0.9.0"。
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

// parseVersion 把 "0.9.0" / "0.9" / "0.9.0-beta.1" 拆成数字段与预发布标签。
func parseVersion(v string) (releaseVersion, bool) {
	v = normalizeVersion(v)
	if v == "" {
		return releaseVersion{}, false
	}
	core := v
	pre := ""
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		core = v[:i]
		pre = v[i+1:]
	}
	parts := strings.Split(core, ".")
	if len(parts) > 4 {
		return releaseVersion{}, false
	}
	out := releaseVersion{pre: pre}
	for i, p := range parts {
		if p == "" {
			return releaseVersion{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return releaseVersion{}, false
		}
		switch i {
		case 0:
			out.major = n
		case 1:
			out.minor = n
		case 2:
			out.patch = n
		case 3:
			out.build = n
		}
	}
	return out, true
}

type releaseVersion struct {
	major, minor, patch, build int
	pre                        string
}

// compareVersion 返回 -1/0/1。预发布版本小于同号正式版（1.0.0-beta < 1.0.0），
// 两个预发布之间按字符串比较——够用，不引 semver 依赖。
func compareVersion(a, b string) int {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return strings.Compare(normalizeVersion(a), normalizeVersion(b))
	}
	for _, pair := range [][2]int{
		{av.major, bv.major}, {av.minor, bv.minor}, {av.patch, bv.patch}, {av.build, bv.build},
	} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	if av.pre == bv.pre {
		return 0
	}
	if av.pre == "" {
		return 1
	}
	if bv.pre == "" {
		return -1
	}
	return strings.Compare(av.pre, bv.pre)
}
