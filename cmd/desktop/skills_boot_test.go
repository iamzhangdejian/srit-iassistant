//go:build !bindings

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/localsandbox/skilltree"
)

func writeBundledSkill(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	md := "---\nname: " + name + "\ndescription: 内置技能\n---\n\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatalf("写 SKILL.md 失败：%v", err)
	}
}

func TestInstallSkillsIntoInstallsAndSkips(t *testing.T) {
	src := t.TempDir()
	writeBundledSkill(t, filepath.Join(src, "office-docs"), "office-docs")
	skillsRoot := t.TempDir()

	if got := installSkillsInto(context.Background(), src, skillsRoot); got != 1 {
		t.Fatalf("首次应装 1 个，实际 %d", got)
	}
	tree, err := skilltree.New(skillsRoot)
	if err != nil {
		t.Fatalf("打开技能树失败：%v", err)
	}
	if !tree.Installed("office-docs") {
		t.Fatal("office-docs 应已安装")
	}

	// 第二次启动：已装过的必须跳过，否则用户改过的脚本会被覆盖。
	if got := installSkillsInto(context.Background(), src, skillsRoot); got != 0 {
		t.Fatalf("重复安装应跳过，实际装了 %d 个", got)
	}
}

func TestInstallSkillsIntoWithoutSource(t *testing.T) {
	skillsRoot := t.TempDir()
	if got := installSkillsInto(context.Background(), filepath.Join(skillsRoot, "不存在"), skillsRoot); got != 0 {
		t.Fatalf("源目录不存在时应装 0 个，实际 %d", got)
	}
}

func TestBundledSkillsDir(t *testing.T) {
	if got := bundledSkillsDir(""); got != "" {
		t.Fatalf("空资源目录应返回空串，实际 %q", got)
	}
	if got := bundledSkillsDir("/tmp/res"); got != "/tmp/res/skills" {
		t.Fatalf("路径拼接不对：%q", got)
	}
}
