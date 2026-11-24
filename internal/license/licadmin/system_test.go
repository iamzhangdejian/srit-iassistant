package licadmin

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeZip 把若干「名字 → 内容」打成 zip，供恢复包测试用。
func writeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("写 zip 条目失败：%v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("写 zip 内容失败：%v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败：%v", err)
	}
	return buf.Bytes()
}

func TestBuildBackupContainsCoreFiles(t *testing.T) {
	dir := t.TempDir()
	// 造出真实数据目录里的几样东西
	if err := os.WriteFile(filepath.Join(dir, "ledger.json"), []byte(`{"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private.key"), []byte("priv"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "public.key"), []byte("pub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "skills", "pkgs", "office-docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "pkgs", "office-docs", "SKILL.md"), []byte("# 技能"), 0o600); err != nil {
		t.Fatal(err)
	}

	db := openTestDB(t)
	out := filepath.Join(t.TempDir(), "backup.zip")
	info, err := BuildBackup(db, dir, out, false, time.Now())
	if err != nil {
		t.Fatalf("生成备份失败：%v", err)
	}
	if info.SizeBytes == 0 {
		t.Fatal("备份大小不该为 0")
	}

	names := strings.Join(info.Entries, ",")
	for _, want := range []string{"licadmin.db", "ledger.json", "private.key", "public.key", "skills/pkgs/office-docs/SKILL.md", "README.txt"} {
		if !strings.Contains(names, want) {
			t.Fatalf("备份应含 %s，实际 %v", want, info.Entries)
		}
	}
	if strings.Contains(names, "releases/") {
		t.Fatal("默认不该打包发行包（一个平台几百 MB）")
	}
}

func TestValidateRestoreZipRejectsTraversal(t *testing.T) {
	cases := map[string]map[string]string{
		"路径穿越":  {"../evil.txt": "x"},
		"绝对路径":  {"/etc/passwd": "x"},
		"未知条目":  {"hack.sh": "x"},
		"符号链接名": {"licadmin.db": "x"}, // 只在内容层面合法，下面单独测 symlink
	}
	for name, files := range cases {
		if name == "符号链接名" {
			continue
		}
		data := writeZip(t, files)
		if _, err := ValidateRestoreZip(data); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		}
	}

	// 合法包：只允许白名单条目
	ok := writeZip(t, map[string]string{
		"licadmin.db":                  "db",
		"ledger.json":                  `{"entries":[]}`,
		"skills/pkgs/office-docs/a.md": "a",
	})
	names, err := ValidateRestoreZip(ok)
	if err != nil {
		t.Fatalf("合法包不该被拒绝：%v", err)
	}
	if len(names) != 3 {
		t.Fatalf("应识别 3 个文件，实际 %v", names)
	}
}

func TestValidateRestoreZipRejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "licadmin.db"}
	hdr.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("/etc/passwd")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRestoreZip(buf.Bytes()); err == nil {
		t.Fatal("符号链接条目应被拒绝——恢复包是外部输入，不能变成读任意文件的通道")
	}
}

func TestStageAndApplyRestore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ledger.json"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "licadmin.log"), []byte("日志不该被动"), 0o600); err != nil {
		t.Fatal(err)
	}

	data := writeZip(t, map[string]string{
		"ledger.json":            `{"entries":["new"]}`,
		"licadmin.db":            "newdb",
		"skills/pkgs/x/SKILL.md": "# x",
	})
	if _, err := StageRestore(dir, data, time.Now()); err != nil {
		t.Fatalf("暂存失败：%v", err)
	}

	pending, _, names := PendingRestore(dir)
	if !pending || len(names) != 3 {
		t.Fatalf("应看到待生效恢复包（3 个文件），实际 pending=%v names=%v", pending, names)
	}

	// 生效前：老数据还在
	if b, _ := os.ReadFile(filepath.Join(dir, "ledger.json")); string(b) != "old" {
		t.Fatalf("应用前不该改动数据，实际 %q", string(b))
	}

	kept, err := ApplyPendingRestore(dir, time.Now())
	if err != nil {
		t.Fatalf("应用失败：%v", err)
	}
	if kept == "" {
		t.Fatal("应返回原数据备份目录")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "ledger.json")); !strings.Contains(string(b), "new") {
		t.Fatalf("应用后应是新数据，实际 %q", string(b))
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "licadmin.db")); string(b) != "newdb" {
		t.Fatalf("数据库应被替换，实际 %q", string(b))
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "pkgs", "x", "SKILL.md")); err != nil {
		t.Fatalf("技能目录应恢复：%v", err)
	}
	// 日志不在恢复清单里，必须原地不动（否则排障信息全丢）
	if b, _ := os.ReadFile(filepath.Join(dir, "licadmin.log")); string(b) != "日志不该被动" {
		t.Fatalf("日志不该被挪走，实际 %q", string(b))
	}
	// 原数据要留档
	if _, err := os.Stat(filepath.Join(kept, "ledger.json")); err != nil {
		t.Fatalf("原数据备份目录应保留旧台账：%v", err)
	}
	// 应用后 stage 必须消失，否则每次启动都会重复覆盖
	if again, _, _ := PendingRestore(dir); again {
		t.Fatal("应用后不应再有待生效恢复包")
	}
}

func TestLogTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "licadmin.log")
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, strings.Repeat("x", 10)+"-"+strconv.Itoa(i))
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, size, err := LogTail(p, 5)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if size == 0 {
		t.Fatal("应返回文件大小")
	}
	if len(got) != 5 {
		t.Fatalf("应返回 5 行，实际 %d 行：%v", len(got), got)
	}
	if !strings.HasSuffix(got[len(got)-1], "-50") {
		t.Fatalf("最后一行应是第 50 行，实际 %q", got[len(got)-1])
	}

	// 文件不存在：返回空而不是报错（命令行模式本来不写文件日志）
	if got, _, err := LogTail(filepath.Join(dir, "nope.log"), 10); err != nil || len(got) != 0 {
		t.Fatalf("缺失日志应返回空：got=%v err=%v", got, err)
	}
}
