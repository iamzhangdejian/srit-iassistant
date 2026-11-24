package licadmin

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 系统页（阶段 4）：备份、恢复、日志。
//
// 备份的目标是「换台机器双击就能继续发码」：台账、账号库、密钥、技能包
// 四样缺一不可。安装包（releases/）默认不打包——一个平台几百 MB，客户
// 每次备份都拷一遍没有意义，需要时用 include=releases 显式带上。
//
// 恢复刻意做成「重启后生效」：进程持有 SQLite 连接和 WAL，运行期直接覆盖
// licadmin.db 会把库写坏。所以上传的包先进 `.pending-restore/`，下次启动
// 在打开数据库之前应用，并把原数据目录整体挪到 `backup-before-restore-<时间>/`。

const (
	pendingRestoreDir   = ".pending-restore"
	pendingRestoreMark  = ".pending-restore/READY"
	backupBeforeRestore = "backup-before-restore-"
	maxRestoreZipBytes  = 1 << 30
)

// 备份/恢复允许出现的顶层条目；其余一律拒绝——恢复包是外部输入，
// 不能让它顺手写出 dataDir 之外的文件，也不能塞进脚本。
var restorableEntries = map[string]bool{
	"ledger.json":   true,
	"licadmin.db":   true,
	"private.key":   true,
	"public.key":    true,
	"skills":        true,
	"releases":      true,
	"README.txt":    true,
	"manifest.json": true,
	"VERSION":       true,
}

// BackupInfo 描述一次备份的内容，界面用它显示「包里有什么」。
type BackupInfo struct {
	Path      string   `json:"path"`
	SizeBytes int64    `json:"size_bytes"`
	Entries   []string `json:"entries"`
	CreatedAt int64    `json:"created_at"`
}

// BuildBackup 把数据目录里可恢复的文件打成一个 zip。
//
// db 用 `VACUUM INTO` 导出快照：直接拷 licadmin.db 会漏掉还在 WAL 里的
// 事务，恢复到新机器上就可能缺最近的发码记录。
func BuildBackup(db *sql.DB, dataDir, outPath string, includeReleases bool, now time.Time) (BackupInfo, error) {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return BackupInfo{}, err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return BackupInfo{}, err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	info := BackupInfo{CreatedAt: now.Unix()}

	addFile := func(name, src string) error {
		in, err := os.Open(src)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil // 文件可能还没生成（比如首次运行没有 ledger.json）
			}
			return err
		}
		defer in.Close()
		st, err := in.Stat()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(st)
		if err != nil {
			return err
		}
		hdr.Name = name
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, in); err != nil {
			return err
		}
		info.Entries = append(info.Entries, name)
		return nil
	}

	// 1) SQLite 快照
	snapshot := filepath.Join(dataDir, ".backup-snapshot.db")
	_ = os.Remove(snapshot)
	if db != nil {
		if _, err := db.Exec(`VACUUM INTO ?`, snapshot); err != nil {
			return BackupInfo{}, fmt.Errorf("导出账号库快照失败：%w", err)
		}
		if err := addFile("licadmin.db", snapshot); err != nil {
			return BackupInfo{}, err
		}
		_ = os.Remove(snapshot)
	}

	// 2) 台账与密钥
	for _, name := range []string{"ledger.json", "private.key", "public.key"} {
		if err := addFile(name, filepath.Join(dataDir, name)); err != nil {
			return BackupInfo{}, err
		}
	}

	// 3) 技能包（小，且丢了就要重传）
	if err := addTree(zw, dataDir, "skills", &info); err != nil {
		return BackupInfo{}, err
	}
	// 4) 发行包（大，按需）
	if includeReleases {
		if err := addTree(zw, dataDir, "releases", &info); err != nil {
			return BackupInfo{}, err
		}
	}

	// 5) 说明文件：拿到 zip 的人应该知道这是什么、怎么用
	readme := fmt.Sprintf(
		"国研智研体授权管理 · 数据备份\n生成时间：%s\n内容：账号库（licadmin.db）、授权台账（ledger.json）、密钥（private.key / public.key）、技能包%s\n\n"+
			"恢复方式：在管理端「系统 → 备份与恢复」上传本文件，重启管理端后生效。\n"+
			"注意：private.key 是签发授权码的私钥，泄漏等于所有人可自签授权，请妥善保管。\n",
		now.Format("2006-01-02 15:04:05"), map[bool]string{true: "、发行包", false: ""}[includeReleases])
	if w, err := zw.Create("README.txt"); err == nil {
		_, _ = io.WriteString(w, readme)
		info.Entries = append(info.Entries, "README.txt")
	}

	if err := zw.Close(); err != nil {
		return BackupInfo{}, err
	}
	st, err := os.Stat(outPath)
	if err != nil {
		return BackupInfo{}, err
	}
	info.Path = outPath
	info.SizeBytes = st.Size()
	return info, nil
}

// addTree 递归把目录写进 zip（保留相对路径，跳过符号链接）。
func addTree(zw *zip.Writer, dataDir, sub string, info *BackupInfo) error {
	root := filepath.Join(dataDir, sub)
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return nil
	}
	return filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(dataDir, p)
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		if _, err := io.Copy(w, in); err != nil {
			return err
		}
		info.Entries = append(info.Entries, hdr.Name)
		return nil
	})
}

// ValidateRestoreZip 只检查不落盘：包里的每个条目都必须是允许的名字或位于允许的目录下，
// 且不得是绝对路径 / 含 `..` / 符号链接。
func ValidateRestoreZip(data []byte) ([]string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("不是有效的 zip：%w", err)
	}
	var names []string
	for _, f := range r.File {
		name := path.Clean(strings.TrimPrefix(f.Name, "./"))
		if name == "." || name == "" {
			continue
		}
		if strings.HasPrefix(f.Name, "/") || strings.Contains(name, "..") {
			return nil, fmt.Errorf("恢复包内含非法路径：%s", f.Name)
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("恢复包内含符号链接：%s", f.Name)
		}
		if f.FileInfo().IsDir() || strings.HasPrefix(f.Name, "__MACOSX/") || strings.HasSuffix(name, ".DS_Store") {
			continue
		}
		top := name
		if i := strings.Index(name, "/"); i >= 0 {
			top = name[:i]
		}
		if _, ok := restorableEntries[top]; !ok {
			return nil, fmt.Errorf("恢复包内含未知条目：%s", f.Name)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, errors.New("恢复包是空的")
	}
	sort.Strings(names)
	return names, nil
}

// StageRestore 把上传的恢复包解到 `<data>/.pending-restore/`，等下次启动生效。
func StageRestore(dataDir string, data []byte, now time.Time) (string, error) {
	if int64(len(data)) > maxRestoreZipBytes {
		return "", fmt.Errorf("恢复包超过 %d MB", maxRestoreZipBytes>>20)
	}
	names, err := ValidateRestoreZip(data)
	if err != nil {
		return "", err
	}

	stage := filepath.Join(dataDir, pendingRestoreDir)
	if err := os.RemoveAll(stage); err != nil {
		return "", err
	}
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return "", err
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, f := range r.File {
		name := path.Clean(strings.TrimPrefix(f.Name, "./"))
		if name == "." || name == "" || f.FileInfo().IsDir() ||
			strings.HasPrefix(f.Name, "__MACOSX/") || strings.HasSuffix(name, ".DS_Store") {
			continue
		}
		dst := filepath.Join(stage, filepath.FromSlash(name))
		// 双重保险：即便上面的校验漏了，这里也不允许写出 stage 之外。
		cleanStage := filepath.Clean(stage) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(dst), cleanStage) {
			return "", fmt.Errorf("恢复条目越界：%s", name)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			rc.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return "", copyErr
		}
	}

	marker := filepath.Join(dataDir, filepath.FromSlash(pendingRestoreMark))
	content := fmt.Sprintf("staged_at=%d\nentries=%d\n", now.Unix(), len(names))
	if err := os.WriteFile(marker, []byte(content), 0o600); err != nil {
		return "", err
	}
	return strings.Join(names, "\n"), nil
}

// PendingRestore 报告是否有等待生效的恢复包。
func PendingRestore(dataDir string) (bool, int64, []string) {
	stage := filepath.Join(dataDir, pendingRestoreDir)
	st, err := os.Stat(stage)
	if err != nil || !st.IsDir() {
		return false, 0, nil
	}
	var names []string
	_ = filepath.Walk(stage, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || fi.Name() == "READY" {
			return nil
		}
		if rel, err := filepath.Rel(stage, p); err == nil {
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(names)
	return true, st.ModTime().Unix(), names
}

// ApplyPendingRestore 在启动时（打开数据库之前）应用恢复包。
//
// 顺序很重要：先把现有文件整体挪到 `backup-before-restore-<时间>/`，
// 再拷新文件。这样即便恢复包不完整，也能从那个目录整体退回。
func ApplyPendingRestore(dataDir string, now time.Time) (string, error) {
	pending, _, names := PendingRestore(dataDir)
	if !pending || len(names) == 0 {
		return "", nil
	}
	stage := filepath.Join(dataDir, pendingRestoreDir)
	keep := filepath.Join(dataDir, backupBeforeRestore+now.Format("20060102-150405"))
	if err := os.MkdirAll(keep, 0o700); err != nil {
		return "", err
	}

	// 1) 挪走现有数据（只挪会被恢复覆盖的顶层条目，避免把日志/发行包一起搬走）
	tops := map[string]bool{}
	for _, name := range names {
		top := name
		if i := strings.Index(name, "/"); i >= 0 {
			top = name[:i]
		}
		tops[top] = true
	}
	for top := range tops {
		src := filepath.Join(dataDir, top)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(keep, top)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		if err := os.Rename(src, dst); err != nil {
			return "", fmt.Errorf("备份现有 %s 失败：%w", top, err)
		}
	}

	// 2) 把 stage 里的文件拷回来
	if err := filepath.Walk(stage, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(stage, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dataDir, rel)
		if fi.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		if fi.Name() == "READY" {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	}); err != nil {
		return "", err
	}

	// 3) 清掉 stage（恢复已完成，留着会下次重复应用）
	if err := os.RemoveAll(stage); err != nil {
		return "", err
	}
	return keep, nil
}

// LogTail 返回日志文件最后 n 行；文件不存在时返回空切片（不是错误——
// 命令行模式本来就不写文件日志）。
func LogTail(logPath string, n int) ([]string, int64, error) {
	if n <= 0 || n > 2000 {
		n = 200
	}
	if strings.TrimSpace(logPath) == "" {
		return nil, 0, nil
	}
	f, err := os.Open(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}

	// 从文件尾部反向扫描，避免把几百 MB 日志读进内存。
	const chunk = 64 << 10
	var (
		offset = st.Size()
		buf    []byte
		lines  []string
	)
	for offset > 0 && len(lines) <= n {
		readSize := int64(chunk)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize
		block := make([]byte, readSize)
		if _, err := f.ReadAt(block, offset); err != nil && !errors.Is(err, io.EOF) {
			return nil, 0, err
		}
		buf = append(block, buf...)
		lines = strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, st.Size(), nil
}
