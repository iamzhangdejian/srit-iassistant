package licadmin

// 技能包管理（阶段 3）。
//
// 这一层只做三件事：把上传的技能包存下来、把它能做什么/不能做什么查清楚、
// 把「已发布」的那几个打成一个包交给客户端打包脚本。它不负责把技能注入
// 沙箱镜像 —— 那是客户端启动时读 Resources/skills 的事（见 internal 侧）。
//
// 为什么校验要先解包再逐文件看：
//   - 技能包最终会被复制进用户的 ~/.weknora/skills 并被 agent 读取，里面
//     带 shell 命令与 Python 脚本；zip 里塞一个 ../.. 就能写到工作区外，
//     所以路径是硬校验而不是提示。
//   - Python 语法错只会在客户机器上炸，且炸在用户的对话里；上传时
//     python3 -m py_compile 一次能省掉最难看的一类售后。
//   - 但不执行任何脚本：管理端跑客户脚本等于把发码机变成执行器，
//     静态检查已经能挡住绝大多数问题。

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 技能包校验的规模上限。数值都取「够用且不至于把磁盘写满」的量级：
// 一个纯文本技能通常几十 KB，带脚本的 office 类也就几 MB。
const (
	maxSkillZipBytes       = 32 << 20 // 上传 zip 本身
	maxSkillUnpackedBytes  = 64 << 20 // 解包后总量
	maxSkillFileBytes      = 8 << 20  // 单个文件
	maxSkillFiles          = 400
	maxSkillDepth          = 6
	maxSkillNameLen        = 64
	maxSkillDescLen        = 1024
	skillManifestName      = "manifest.json"
	skillEntryName         = "SKILL.md"
	skillCheckTimeout      = 20 * time.Second
	skillStatusDraft       = "draft"
	skillStatusPublished   = "published"
	skillFindingError      = "error"
	skillFindingWarn       = "warn"
	skillFindingInfo       = "info"
	skillPythonUnavailable = "python3 不可用，已跳过语法检查"
)

// SkillFinding 是一条校验结论。
type SkillFinding struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

// SkillPackage 是一条技能记录。
type SkillPackage struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Version     string         `json:"version"`
	Author      string         `json:"author,omitempty"`
	Status      string         `json:"status"`
	SHA256      string         `json:"sha256"`
	SizeBytes   int64          `json:"size_bytes"`
	FileCount   int            `json:"file_count"`
	Dir         string         `json:"-"`
	UploadedAt  int64          `json:"uploaded_at"`
	PublishedAt int64          `json:"published_at,omitempty"`
	CheckedAt   int64          `json:"checked_at,omitempty"`
	CheckedOK   bool           `json:"checked_ok"`
	Findings    []SkillFinding `json:"findings"`
}

// SkillValidation 是解包校验的完整结果。
type SkillValidation struct {
	Name        string
	Title       string
	Description string
	Version     string
	Author      string
	FileCount   int
	SizeBytes   int64
	Findings    []SkillFinding
	// Files 是相对路径清单，界面详情页展示用。
	Files []string
}

// OK 表示没有 error 级结论。
func (v SkillValidation) OK() bool {
	for _, f := range v.Findings {
		if f.Level == skillFindingError {
			return false
		}
	}
	return true
}

// SkillStore 是技能库（SQLite，与账号/审计同库）。
type SkillStore struct {
	db      *sql.DB
	dataDir string
}

// NewSkillStore 复用账号库连接（不建表，测试里直接构造时用）。
func NewSkillStore(db *sql.DB, dataDir string) *SkillStore {
	return &SkillStore{db: db, dataDir: dataDir}
}

// NewSkillStoreForData 复用一个数据目录里的账号库连接并完成建表。
func NewSkillStoreForData(db *sql.DB, dataDir string) (*SkillStore, error) {
	s := NewSkillStore(db, dataDir)
	if err := s.migrateSkills(); err != nil {
		return nil, err
	}
	return s, nil
}

// migrateSkills 建表。放在 AuthStore.migrate 之后调用。
func (s *SkillStore) migrateSkills() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS skill (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT NOT NULL,
  title        TEXT NOT NULL DEFAULT '',
  description  TEXT NOT NULL DEFAULT '',
  version      TEXT NOT NULL DEFAULT '0.0.0',
  author       TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL DEFAULT 'draft',
  sha256       TEXT NOT NULL,
  size_bytes   INTEGER NOT NULL DEFAULT 0,
  file_count   INTEGER NOT NULL DEFAULT 0,
  dir          TEXT NOT NULL,
  uploaded_at  INTEGER NOT NULL,
  published_at INTEGER NOT NULL DEFAULT 0,
  checked_at   INTEGER NOT NULL DEFAULT 0,
  checked_ok   INTEGER NOT NULL DEFAULT 0,
  findings     TEXT NOT NULL DEFAULT '[]'
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_skill_name_version ON skill(name, version);
CREATE INDEX IF NOT EXISTS idx_skill_status ON skill(status);
`
	if _, err := s.db.Exec(ddl); err != nil {
		return fmt.Errorf("建技能表失败：%w", err)
	}
	return nil
}

// skillsDir 是技能包落盘根目录。
func (s *SkillStore) skillsDir() string { return filepath.Join(s.dataDir, "skills") }

// List 返回全部技能（新上传的在前）。
func (s *SkillStore) List() ([]SkillPackage, error) {
	rows, err := s.db.Query(`SELECT id, name, title, description, version, author, status, sha256,
		size_bytes, file_count, dir, uploaded_at, published_at, checked_at, checked_ok, findings
		FROM skill ORDER BY uploaded_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkillPackage{}
	for rows.Next() {
		var p SkillPackage
		var findings string
		var checkedOK int
		if err := rows.Scan(&p.ID, &p.Name, &p.Title, &p.Description, &p.Version, &p.Author, &p.Status,
			&p.SHA256, &p.SizeBytes, &p.FileCount, &p.Dir, &p.UploadedAt, &p.PublishedAt,
			&p.CheckedAt, &checkedOK, &findings); err != nil {
			return nil, err
		}
		p.CheckedOK = checkedOK == 1
		p.Findings = []SkillFinding{}
		_ = json.Unmarshal([]byte(findings), &p.Findings)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get 按 id 取一条。
func (s *SkillStore) Get(id int64) (SkillPackage, error) {
	var p SkillPackage
	var findings string
	var checkedOK int
	err := s.db.QueryRow(`SELECT id, name, title, description, version, author, status, sha256,
		size_bytes, file_count, dir, uploaded_at, published_at, checked_at, checked_ok, findings
		FROM skill WHERE id = ?`, id).Scan(&p.ID, &p.Name, &p.Title, &p.Description, &p.Version,
		&p.Author, &p.Status, &p.SHA256, &p.SizeBytes, &p.FileCount, &p.Dir, &p.UploadedAt,
		&p.PublishedAt, &p.CheckedAt, &checkedOK, &findings)
	if errors.Is(err, sql.ErrNoRows) {
		return SkillPackage{}, errors.New("技能不存在")
	}
	if err != nil {
		return SkillPackage{}, err
	}
	p.CheckedOK = checkedOK == 1
	p.Findings = []SkillFinding{}
	_ = json.Unmarshal([]byte(findings), &p.Findings)
	return p, nil
}

// Import 校验并落盘一个上传的技能包。
//
// 同名同版本重复上传直接拒绝：覆盖会让「已发布的是哪份字节」变得不可追，
// 出问题时要能按 sha256 说清楚客户端拿到的到底是哪一版。
func (s *SkillStore) Import(zipName string, data []byte, now time.Time) (SkillPackage, error) {
	if len(data) == 0 {
		return SkillPackage{}, errors.New("技能包为空")
	}
	if len(data) > maxSkillZipBytes {
		return SkillPackage{}, fmt.Errorf("技能包超过 %d MB 上限", maxSkillZipBytes>>20)
	}
	digest := sha256.Sum256(data)
	sum := hex.EncodeToString(digest[:])

	val, files, err := ValidateSkillZip(zipName, data)
	if err != nil {
		return SkillPackage{}, err
	}
	name, version := val.Name, val.Version
	if strings.TrimSpace(name) == "" {
		return SkillPackage{}, errors.New("技能包缺少 name（SKILL.md frontmatter 或 manifest.json）")
	}
	if err := checkSkillFolderName(name); err != nil {
		return SkillPackage{}, err
	}
	if strings.TrimSpace(version) == "" {
		version = "0.0.0"
	}

	var exists int
	if err := s.db.QueryRow(`SELECT count(1) FROM skill WHERE name = ? AND version = ?`,
		name, version).Scan(&exists); err != nil {
		return SkillPackage{}, err
	}
	if exists > 0 {
		return SkillPackage{}, fmt.Errorf("技能 %s 的 %s 版本已存在；要替换请先删除旧记录", name, version)
	}

	target := filepath.Join(s.skillsDir(), "pkgs", fmt.Sprintf("%s@%s", name, version))
	// 上一步已经保证 name/version 不含路径分隔符，这里再确认一次，
	// 因为 target 会参与 mkdir 与文件写入。
	if !strings.HasPrefix(target, filepath.Join(s.skillsDir(), "pkgs")+string(os.PathSeparator)) {
		return SkillPackage{}, errors.New("技能落盘路径非法")
	}
	if err := os.RemoveAll(target); err != nil {
		return SkillPackage{}, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return SkillPackage{}, err
	}
	if err := extractSkillFiles(data, target); err != nil {
		_ = os.RemoveAll(target)
		return SkillPackage{}, err
	}

	// 原始 zip 也留一份：打包给客户时用原始字节，能复现 sha256。
	zipDir := filepath.Join(s.skillsDir(), "zips")
	if err := os.MkdirAll(zipDir, 0o755); err != nil {
		return SkillPackage{}, err
	}
	zipPath := filepath.Join(zipDir, fmt.Sprintf("%s@%s-%s.zip", name, version, sum[:8]))
	if err := os.WriteFile(zipPath, data, 0o644); err != nil {
		return SkillPackage{}, err
	}

	findingsJSON, _ := json.Marshal(val.Findings)
	checkedOK := 0
	if val.OK() {
		checkedOK = 1
	}
	res, err := s.db.Exec(`INSERT INTO skill
		(name, title, description, version, author, status, sha256, size_bytes, file_count, dir,
		 uploaded_at, checked_at, checked_ok, findings)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		name, val.Title, val.Description, version, val.Author, skillStatusDraft, sum,
		val.SizeBytes, len(files), target, now.Unix(), now.Unix(), checkedOK, string(findingsJSON))
	if err != nil {
		_ = os.RemoveAll(target)
		return SkillPackage{}, err
	}
	id, _ := res.LastInsertId()
	return s.Get(id)
}

// Publish 把技能标为已发布。校验未通过的包不允许发布 —— 发布意味着它会被
// 打进客户端的 Resources/skills，带着一个必然报错的脚本发布等于把问题推给客户。
func (s *SkillStore) Publish(id int64, now time.Time) (SkillPackage, error) {
	p, err := s.Get(id)
	if err != nil {
		return SkillPackage{}, err
	}
	if !p.CheckedOK {
		return SkillPackage{}, errors.New("校验未通过，不能发布；请修掉 error 级问题后重新上传")
	}
	if _, err := s.db.Exec(`UPDATE skill SET status = ?, published_at = ? WHERE id = ?`,
		skillStatusPublished, now.Unix(), id); err != nil {
		return SkillPackage{}, err
	}
	// 同一技能只保留一个发布版本：换版时旧版本退回草稿，避免客户端同时
	// 拿到同名技能的两份实现（skilltree 按名字装，装谁取决于循环顺序）。
	if _, err := s.db.Exec(`UPDATE skill SET status = ? WHERE name = ? AND id <> ? AND status = ?`,
		skillStatusDraft, p.Name, id, skillStatusPublished); err != nil {
		return SkillPackage{}, err
	}
	return s.Get(id)
}

// Unpublish 撤回发布。
func (s *SkillStore) Unpublish(id int64) (SkillPackage, error) {
	if _, err := s.db.Exec(`UPDATE skill SET status = ?, published_at = 0 WHERE id = ?`,
		skillStatusDraft, id); err != nil {
		return SkillPackage{}, err
	}
	return s.Get(id)
}

// Delete 删除记录与解包目录（原始 zip 保留，便于事后比对）。
func (s *SkillStore) Delete(id int64) error {
	p, err := s.Get(id)
	if err != nil {
		return err
	}
	if p.Status == skillStatusPublished {
		return errors.New("已发布的技能不能直接删除，请先撤回发布")
	}
	if p.Dir != "" {
		_ = os.RemoveAll(p.Dir)
	}
	_, err = s.db.Exec(`DELETE FROM skill WHERE id = ?`, id)
	return err
}

// Bundle 把已发布的技能打成 zip，交给客户端打包脚本注入 Resources/skills。
//
// 布局固定为 <name>/...，与 skilltree 的目录约定一致；另放一份
// .manifest.json 记录每个技能的名称/版本/sha256，方便发版时核对。
func (s *SkillStore) Bundle(now time.Time) ([]byte, string, error) {
	rows, err := s.db.Query(`SELECT name, version, sha256, dir FROM skill WHERE status = ? ORDER BY name`,
		skillStatusPublished)
	if err != nil {
		return nil, "", err
	}
	type item struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	}
	var items []item
	var dirs []string
	for rows.Next() {
		var it item
		var dir string
		if err := rows.Scan(&it.Name, &it.Version, &it.SHA256, &dir); err != nil {
			rows.Close()
			return nil, "", err
		}
		items = append(items, it)
		dirs = append(dirs, dir)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(items) == 0 {
		return nil, "", errors.New("还没有已发布的技能；请先发布至少一个")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, it := range items {
		root := dirs[i]
		// 逐个文件写，保持 <name>/<相对路径> 的布局。
		err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			w, err := zw.Create(path.Join(it.Name, filepath.ToSlash(rel)))
			if err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(w, f)
			return err
		})
		if err != nil {
			zw.Close()
			return nil, "", fmt.Errorf("打包技能 %s 失败：%w", it.Name, err)
		}
	}
	manifest, _ := json.MarshalIndent(map[string]any{
		"generated_at": now.Unix(),
		"skills":       items,
	}, "", "  ")
	w, err := zw.Create(".manifest.json")
	if err != nil {
		zw.Close()
		return nil, "", err
	}
	if _, err := w.Write(manifest); err != nil {
		zw.Close()
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	name := fmt.Sprintf("skills-bundle-%s.zip", now.Format("20060102-1504"))
	return buf.Bytes(), name, nil
}

// ---------- 校验流水线 ----------

var skillFolderNameRE = regexp.MustCompile(`^[\p{L}\p{N}_-]+$`)

// checkSkillFolderName 保证技能名能直接当目录名用（与客户端的
// skilltree.validName 同口径），杜绝 name 里藏路径分隔符。
func checkSkillFolderName(name string) error {
	if name == "" || len(name) > maxSkillNameLen {
		return fmt.Errorf("技能名长度必须在 1~%d 之间", maxSkillNameLen)
	}
	if !skillFolderNameRE.MatchString(name) {
		return errors.New("技能名只能包含字母、数字、下划线和连字符")
	}
	if name == "." || name == ".." {
		return errors.New("技能名不能是 . 或 ..")
	}
	return nil
}

// ValidateSkillZip 解包并检查技能包，不写盘、不执行脚本。
func ValidateSkillZip(zipName string, data []byte) (SkillValidation, []string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return SkillValidation{}, nil, fmt.Errorf("%s 不是有效的 zip：%w", zipName, err)
	}
	val := SkillValidation{Findings: []SkillFinding{}}
	add := func(level, code, msg, p string) {
		val.Findings = append(val.Findings, SkillFinding{Level: level, Code: code, Message: msg, Path: p})
	}

	if len(zr.File) == 0 {
		return val, nil, errors.New("zip 里没有任何文件")
	}
	if len(zr.File) > maxSkillFiles {
		return val, nil, fmt.Errorf("文件数 %d 超过上限 %d", len(zr.File), maxSkillFiles)
	}

	var total int64
	var files []string
	hasEntry := false
	hasManifest := false
	var manifest struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Author      string `json:"author"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	type scriptFile struct {
		tmp string
		rel string
	}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean := path.Clean(f.Name)
		if strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, `\`) {
			add(skillFindingError, "path/absolute", "包内路径不能是绝对路径或含反斜杠", f.Name)
			continue
		}
		if clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			add(skillFindingError, "path/traversal", "包内路径不能跳出技能目录", f.Name)
			continue
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			add(skillFindingError, "path/symlink", "技能包里不允许符号链接（会被解到目标目录之外）", f.Name)
			continue
		}
		depth := strings.Count(clean, "/")
		if depth > maxSkillDepth {
			add(skillFindingError, "path/depth", fmt.Sprintf("目录层级超过 %d 层", maxSkillDepth), clean)
			continue
		}
		if f.UncompressedSize64 > maxSkillFileBytes {
			add(skillFindingError, "size/file", fmt.Sprintf("单文件超过 %d MB", maxSkillFileBytes>>20), clean)
			continue
		}
		total += int64(f.UncompressedSize64)
		if total > maxSkillUnpackedBytes {
			return val, nil, fmt.Errorf("解包后总量超过 %d MB 上限", maxSkillUnpackedBytes>>20)
		}
		files = append(files, clean)

		base := path.Base(clean)
		content := readZipFile(f)
		if base == skillEntryName {
			hasEntry = true
			parseSkillFrontmatter(&val, content, add)
		}
		if base == skillManifestName && depth <= 1 {
			hasManifest = true
			if err := json.Unmarshal(content, &manifest); err != nil {
				add(skillFindingError, "manifest/json", "manifest.json 不是合法 JSON", clean)
			}
		}
	}

	if !hasEntry {
		add(skillFindingError, "entry/missing", "缺少 SKILL.md（技能说明入口）", "")
	}
	if hasManifest {
		if val.Name == "" {
			val.Name = manifest.Name
		}
		if val.Title == "" {
			val.Title = manifest.Title
		}
		if val.Description == "" {
			val.Description = manifest.Description
		}
		if manifest.Version != "" {
			val.Version = manifest.Version
		}
		if manifest.Author != "" {
			val.Author = manifest.Author
		}
		if val.Version == "" {
			add(skillFindingWarn, "manifest/version", "manifest.json 没写 version，按 0.0.0 记录", skillManifestName)
		}
	} else {
		add(skillFindingInfo, "manifest/absent", "没有 manifest.json，版本按 0.0.0 记录（建议补一份，便于发版核对）", "")
	}

	if val.Title == "" {
		val.Title = val.Name
	}

	if val.Title != "" && len([]rune(val.Title)) > maxSkillNameLen {
		add(skillFindingWarn, "frontmatter/title", fmt.Sprintf("标题超过 %d 字符，界面里会被截断", maxSkillNameLen), skillEntryName)
	}
	if val.Description != "" && len([]rune(val.Description)) > maxSkillDescLen {
		add(skillFindingWarn, "frontmatter/description",
			fmt.Sprintf("描述超过 %d 字符，模型选择技能时看的是这段，建议压缩", maxSkillDescLen), skillEntryName)
	}

	// 脚本内容静态检查：这里只扫文本，不执行。
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean := path.Clean(f.Name)
		switch strings.ToLower(path.Ext(clean)) {
		case ".py", ".sh", ".bash", ".js", ".mjs":
			scanSkillScript(clean, readZipFile(f), add)
		}
	}

	// Python 语法检查：解到临时目录跑 py_compile，跑完即删。
	pyFiles := []string{}
	for _, p := range files {
		if strings.ToLower(path.Ext(p)) == ".py" {
			pyFiles = append(pyFiles, p)
		}
	}
	if len(pyFiles) > 0 {
		if _, err := exec.LookPath("python3"); err != nil {
			add(skillFindingWarn, "python/unavailable", skillPythonUnavailable, "")
		} else {
			tmp, err := os.MkdirTemp("", "licadmin-skill-*")
			if err != nil {
				add(skillFindingWarn, "python/temp", "无法创建临时目录，已跳过语法检查", "")
			} else {
				defer os.RemoveAll(tmp)
				for _, f := range zr.File {
					if f.FileInfo().IsDir() {
						continue
					}
					clean := path.Clean(f.Name)
					if strings.ToLower(path.Ext(clean)) != ".py" {
						continue
					}
					dst := filepath.Join(tmp, filepath.FromSlash(clean))
					if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
						continue
					}
					if err := os.WriteFile(dst, readZipFile(f), 0o644); err != nil {
						continue
					}
					if msg := pyCompile(dst); msg != "" {
						add(skillFindingError, "python/syntax", msg, clean)
					}
				}
			}
		}
	}

	val.FileCount = len(files)
	val.SizeBytes = total
	sort.Strings(files)
	if val.Name == "" {
		// 没有 frontmatter 也没 manifest 时，用上传文件名兜底，让记录可辨识。
		val.Name = strings.TrimSuffix(filepath.Base(zipName), filepath.Ext(zipName))
	}
	return val, files, nil
}

// parseSkillFrontmatter 取 SKILL.md 的 --- 头里的 name/title/description。
//
// 刻意不引 YAML 解析器：这里只需要三个标量字段，而技能包的 frontmatter
// 常带手工编写的不规范内容（未加引号的冒号、缩进错位）。客户端有一套
// 修复逻辑，管理端只要「能看出这段说明是给谁的」就够了。
func parseSkillFrontmatter(val *SkillValidation, content []byte, add func(level, code, msg, p string)) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---") {
		add(skillFindingWarn, "frontmatter/missing", "SKILL.md 没有 --- 开头的 frontmatter，技能名将按文件名兜底", skillEntryName)
		return
	}
	end := strings.Index(text[3:], "\n---")
	if end < 0 {
		add(skillFindingError, "frontmatter/unterminated", "SKILL.md 的 frontmatter 没有结束标记 ---", skillEntryName)
		return
	}
	block := text[3 : 3+end]
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		value := strings.TrimSpace(v)
		value = strings.Trim(value, `"'`)
		switch key {
		case "name":
			// 只填 Name：Title 留给显式 title（或 manifest）用，
			// 否则 manifest 里的中文标题永远抢不过这里的兜底值。
			val.Name = value
		case "title":
			val.Title = value
		case "description":
			val.Description = value
		}
	}
	if val.Name == "" {
		add(skillFindingWarn, "frontmatter/name", "frontmatter 里没有 name", skillEntryName)
	}
	if val.Description == "" {
		add(skillFindingWarn, "frontmatter/description", "frontmatter 里没有 description（模型靠它决定要不要用这个技能）", skillEntryName)
	}
	if strings.Contains(strings.ToLower(val.Name), "claude") || strings.Contains(strings.ToLower(val.Name), "anthropic") {
		add(skillFindingWarn, "frontmatter/reserved", "技能名含保留词（claude/anthropic），客户端可能拒绝加载", skillEntryName)
	}
}

// scanSkillScript 扫脚本里的危险写法。
//
// 只按「明显会出事」判断，不做安全沙箱承诺：真正执行时还有宿主沙箱
// 与审批模式兜底，这里的目标是别让明显有害的东西进客户端。
func scanSkillScript(rel string, content []byte, add func(level, code, msg, p string)) {
	text := strings.ToLower(string(content))
	type rule struct {
		needle string
		code   string
		level  string
		msg    string
	}
	rules := []rule{
		{"sudo ", "script/sudo", skillFindingError, "脚本里不能调用 sudo（客户端没有提权能力，会直接失败）"},
		{"rm -rf /", "script/rmrf", skillFindingError, "脚本里不能出现 rm -rf /"},
		{"curl", "script/curl", skillFindingWarn, "脚本里用 curl 下载内容后执行，需人工确认来源可信"},
		{"wget", "script/wget", skillFindingWarn, "脚本里用 wget 下载内容后执行，需人工确认来源可信"},
		{"chmod 777", "script/chmod", skillFindingWarn, "chmod 777 会让技能目录对同机其他用户可写"},
		{"/users/", "script/abspath", skillFindingWarn, "脚本里写死了 /Users/ 绝对路径，换机器会失效"},
		{"/home/", "script/abspath", skillFindingWarn, "脚本里写死了 /home/ 绝对路径，换机器会失效"},
	}
	for _, r := range rules {
		if strings.Contains(text, r.needle) {
			add(r.level, r.code, r.msg, rel)
		}
	}
	if strings.Contains(text, "| sh") || strings.Contains(text, "| bash") {
		add(skillFindingWarn, "script/pipe-shell", "存在「下载后直接交给 shell」的写法", rel)
	}
}

// pyCompile 用 python3 -m py_compile 检查语法，返回错误文本（成功为空）。
func pyCompile(file string) string {
	ctx, cancel := context.WithTimeout(context.Background(), skillCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-m", "py_compile", file)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		// py_compile 的报错含临时路径，去掉以免误导排查。
		msg = strings.ReplaceAll(msg, file, path.Base(file))
		return "Python 语法错误：" + firstLine(msg)
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func readZipFile(f *zip.File) []byte {
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	defer rc.Close()
	// 上限已经在前面按 UncompressedSize64 检查过，这里再加一层防止 header 说谎。
	limited := io.LimitReader(rc, maxSkillFileBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil
	}
	return data
}

// extractSkillFiles 把包内文件按「去掉最外层目录」的规则写到 target。
func extractSkillFiles(data []byte, target string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	strip := commonSkillRoot(zr)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean := path.Clean(f.Name)
		rel := clean
		if strip != "" {
			rel = strings.TrimPrefix(strings.TrimPrefix(clean, strip), "/")
		}
		if rel == "" || rel == "." {
			continue
		}
		dst := filepath.Join(target, filepath.FromSlash(rel))
		if !strings.HasPrefix(dst, target+string(os.PathSeparator)) && dst != target {
			return fmt.Errorf("包内路径 %s 会写到技能目录之外", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, readZipFile(f), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// commonSkillRoot 判断包内是否所有文件都在同一个顶层目录下（常见的
// `my-skill/SKILL.md` 打包方式），是则返回该目录名，写入时剥掉。
func commonSkillRoot(zr *zip.Reader) string {
	root := ""
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean := path.Clean(f.Name)
		first, _, ok := strings.Cut(clean, "/")
		if !ok {
			return "" // 有文件直接位于包根，说明没有统一外壳
		}
		if root == "" {
			root = first
			continue
		}
		if first != root {
			return ""
		}
	}
	return root
}
