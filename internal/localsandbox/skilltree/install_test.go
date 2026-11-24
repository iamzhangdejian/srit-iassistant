package skilltree

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSkill 造一个最小技能目录。
func writeSkillDir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	md := "---\nname: " + name + "\ndescription: 测试用\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatalf("写 SKILL.md 失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "run.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatalf("写脚本失败：%v", err)
	}
}

func TestInstallFromDirActivatesSkill(t *testing.T) {
	root := t.TempDir()
	tree, err := New(root)
	if err != nil {
		t.Fatalf("建树失败：%v", err)
	}
	src := filepath.Join(t.TempDir(), "office-docs")
	writeSkillDir(t, src, "office-docs")

	if _, err := tree.InstallFromDir("office-docs", src); err != nil {
		t.Fatalf("安装失败：%v", err)
	}
	if !tree.Installed("office-docs") {
		t.Fatal("安装后 Installed 应为 true")
	}
	// 内容也要真到位，不能只建了链接。
	link, err := tree.LinkPath("office-docs")
	if err != nil {
		t.Fatalf("取链接失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(link, "scripts", "run.py")); err != nil {
		t.Fatalf("脚本没被复制进去：%v", err)
	}
}

func TestInstallFromDirPrunesOlderVersions(t *testing.T) {
	root := t.TempDir()
	tree, err := New(root)
	if err != nil {
		t.Fatalf("建树失败：%v", err)
	}
	src := filepath.Join(t.TempDir(), "kb-helper")
	writeSkillDir(t, src, "kb-helper")

	first, err := tree.InstallFromDir("kb-helper", src)
	if err != nil {
		t.Fatalf("第一次安装失败：%v", err)
	}
	second, err := tree.InstallFromDir("kb-helper", src)
	if err != nil {
		t.Fatalf("第二次安装失败：%v", err)
	}
	if first == second {
		t.Fatalf("两次安装应落在不同版本目录：%s", first)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("旧版本应被清理，实际仍在：%v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("新版本应存在：%v", err)
	}
}

func TestInstallFromDirRejectsMissingSkillMD(t *testing.T) {
	root := t.TempDir()
	tree, err := New(root)
	if err != nil {
		t.Fatalf("建树失败：%v", err)
	}
	src := t.TempDir()
	if _, err := tree.InstallFromDir("empty", src); err == nil {
		t.Fatal("没有 SKILL.md 时应报错")
	}
	// 失败不该留下半成品版本。
	dirs, _, err := tree.versionDirs("empty")
	if err != nil {
		t.Fatalf("列版本失败：%v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("失败后不应留版本目录，实际 %v", dirs)
	}
}

func TestInstallFromDirRejectsBadName(t *testing.T) {
	root := t.TempDir()
	tree, err := New(root)
	if err != nil {
		t.Fatalf("建树失败：%v", err)
	}
	src := filepath.Join(t.TempDir(), "x")
	writeSkillDir(t, src, "x")
	if _, err := tree.InstallFromDir("../escape", src); err == nil {
		t.Fatal("非法技能名应被拒绝")
	}
}

func TestBundledNamesListsOnlySkills(t *testing.T) {
	dir := t.TempDir()
	writeSkillDir(t, filepath.Join(dir, "office-docs"), "office-docs")
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	names, err := BundledNames(dir)
	if err != nil {
		t.Fatalf("列目录失败：%v", err)
	}
	if len(names) != 1 || names[0] != "office-docs" {
		t.Fatalf("应只列出带 SKILL.md 的目录，实际 %v", names)
	}
	if got, err := BundledNames(filepath.Join(dir, "不存在")); err != nil || len(got) != 0 {
		t.Fatalf("目录不存在时应返回空且不报错，实际 %v %v", got, err)
	}
}
