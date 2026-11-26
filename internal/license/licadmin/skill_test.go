package licadmin

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildZip 用「相对路径 → 内容」造一个测试用压缩包。
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("建 zip 条目失败：%v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("写 zip 条目失败：%v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败：%v", err)
	}
	return buf.Bytes()
}

const goodSkillMD = `---
name: office-docs
description: 生成 Word/PPT/Excel 文件
---

# 用法

调用 scripts/make_docx.py。
`

func newSkillTestStore(t *testing.T) (*SkillStore, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "licadmin.db")+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewSkillStoreForData(db, dir)
	if err != nil {
		t.Fatalf("建技能库失败：%v", err)
	}
	return store, dir
}

func levels(val SkillValidation) (errs, warns int) {
	for _, f := range val.Findings {
		switch f.Level {
		case skillFindingError:
			errs++
		case skillFindingWarn:
			warns++
		}
	}
	return
}

func hasFinding(val SkillValidation, code string) bool {
	for _, f := range val.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestValidateSkillZipHappyPath(t *testing.T) {
	data := buildZip(t, map[string]string{
		"office-docs/SKILL.md":             goodSkillMD,
		"office-docs/manifest.json":        `{"name":"office-docs","version":"1.2.0","author":"国研"}`,
		"office-docs/scripts/make_docx.py": "print('ok')\n",
	})
	val, files, err := ValidateSkillZip("office-docs.zip", data)
	if err != nil {
		t.Fatalf("校验失败：%v", err)
	}
	if !val.OK() {
		t.Fatalf("正常包不应有 error，实际：%+v", val.Findings)
	}
	if val.Name != "office-docs" || val.Version != "1.2.0" || val.Author != "国研" {
		t.Fatalf("元信息解析不对：%+v", val)
	}
	if val.Description != "生成 Word/PPT/Excel 文件" {
		t.Fatalf("描述解析不对：%q", val.Description)
	}
	if len(files) != 3 {
		t.Fatalf("文件数应为 3，实际 %d（%v）", len(files), files)
	}
}

func TestValidateSkillZipRejectsTraversal(t *testing.T) {
	data := buildZip(t, map[string]string{
		"office-docs/SKILL.md": goodSkillMD,
		"../escape.txt":        "pwn",
	})
	val, _, err := ValidateSkillZip("bad.zip", data)
	if err != nil {
		t.Fatalf("不该直接报错：%v", err)
	}
	if val.OK() {
		t.Fatal("带 ../ 的包必须判为不通过")
	}
	if !hasFinding(val, "path/traversal") {
		t.Fatalf("应报 path/traversal，实际 %+v", val.Findings)
	}
}

func TestValidateSkillZipRejectsAbsolutePath(t *testing.T) {
	data := buildZip(t, map[string]string{
		"office-docs/SKILL.md": goodSkillMD,
		"/etc/passwd":          "x",
	})
	val, _, _ := ValidateSkillZip("bad.zip", data)
	if val.OK() {
		t.Fatal("绝对路径必须判为不通过")
	}
}

func TestValidateSkillZipRequiresEntry(t *testing.T) {
	data := buildZip(t, map[string]string{"readme.md": "hi"})
	val, _, _ := ValidateSkillZip("x.zip", data)
	if val.OK() {
		t.Fatal("缺 SKILL.md 必须判为不通过")
	}
	if !hasFinding(val, "entry/missing") {
		t.Fatalf("应报 entry/missing，实际 %+v", val.Findings)
	}
}

func TestValidateSkillZipFlagsDangerousScripts(t *testing.T) {
	data := buildZip(t, map[string]string{
		"office-docs/SKILL.md":       goodSkillMD,
		"office-docs/scripts/run.sh": "sudo rm -rf /tmp/x\n",
		"office-docs/scripts/p.py":   "import os\nprint(os.getcwd())\n",
	})
	val, _, _ := ValidateSkillZip("x.zip", data)
	if val.OK() {
		t.Fatal("含 sudo 的脚本必须判为不通过")
	}
	if !hasFinding(val, "script/sudo") {
		t.Fatalf("应报 script/sudo，实际 %+v", val.Findings)
	}
}

func TestValidateSkillZipPythonSyntax(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("没有 python3，跳过语法检查用例")
	}
	data := buildZip(t, map[string]string{
		"office-docs/SKILL.md":          goodSkillMD,
		"office-docs/scripts/broken.py": "def f(:\n  pass\n",
	})
	val, _, _ := ValidateSkillZip("x.zip", data)
	if val.OK() {
		t.Fatal("语法错的 Python 必须判为不通过")
	}
	if !hasFinding(val, "python/syntax") {
		t.Fatalf("应报 python/syntax，实际 %+v", val.Findings)
	}
}

func TestSkillImportPublishAndBundle(t *testing.T) {
	store, dir := newSkillTestStore(t)
	now := time.Unix(1790000000, 0)
	data := buildZip(t, map[string]string{
		"office-docs/SKILL.md":      goodSkillMD,
		"office-docs/manifest.json": `{"name":"office-docs","version":"1.0.0"}`,
	})

	pkg, err := store.Import("office-docs.zip", data, now)
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if pkg.Status != skillStatusDraft || !pkg.CheckedOK {
		t.Fatalf("刚导入应是校验通过的草稿：%+v", pkg)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "pkgs", "office-docs@1.0.0", "SKILL.md")); err != nil {
		t.Fatalf("解包目录里应有 SKILL.md：%v", err)
	}

	// 同名同版本不允许覆盖。
	if _, err := store.Import("office-docs.zip", data, now); err == nil {
		t.Fatal("重复导入同名同版本应失败")
	}

	// 未发布时导出应报错。
	if _, _, err := store.Bundle(now); err == nil {
		t.Fatal("没有已发布技能时导出应失败")
	}

	published, err := store.Publish(pkg.ID, now)
	if err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if published.Status != skillStatusPublished || published.PublishedAt == 0 {
		t.Fatalf("发布状态不对：%+v", published)
	}

	blob, name, err := store.Bundle(now)
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	if !strings.HasSuffix(name, ".zip") {
		t.Fatalf("导出文件名不对：%s", name)
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("导出的不是有效 zip：%v", err)
	}
	seenSkill, seenManifest := false, false
	for _, f := range zr.File {
		switch f.Name {
		case "office-docs/SKILL.md":
			seenSkill = true
		case ".manifest.json":
			seenManifest = true
		}
	}
	if !seenSkill || !seenManifest {
		t.Fatalf("导出包结构不对：skill=%v manifest=%v", seenSkill, seenManifest)
	}

	// 已发布不能直接删除。
	if err := store.Delete(pkg.ID); err == nil {
		t.Fatal("已发布技能不应允许直接删除")
	}
	if _, err := store.Unpublish(pkg.ID); err != nil {
		t.Fatalf("撤回失败：%v", err)
	}
	if err := store.Delete(pkg.ID); err != nil {
		t.Fatalf("撤回后应该能删除：%v", err)
	}
}

func TestSkillPublishRejectsFailedValidation(t *testing.T) {
	store, _ := newSkillTestStore(t)
	now := time.Unix(1790000000, 0)
	data := buildZip(t, map[string]string{
		"office-docs/SKILL.md":     goodSkillMD,
		"office-docs/scripts/x.sh": "sudo whatever\n",
	})
	pkg, err := store.Import("x.zip", data, now)
	if err != nil {
		t.Fatalf("导入应当成功（校验失败也要留档）：%v", err)
	}
	if pkg.CheckedOK {
		t.Fatal("校验未通过时 CheckedOK 应为 false")
	}
	if _, err := store.Publish(pkg.ID, now); err == nil {
		t.Fatal("校验未通过的包不允许发布")
	}
}

func TestSkillPublishKeepsSingleVersionPerName(t *testing.T) {
	store, _ := newSkillTestStore(t)
	now := time.Unix(1790000000, 0)
	mk := func(version string) SkillPackage {
		data := buildZip(t, map[string]string{
			"office-docs/SKILL.md":      goodSkillMD,
			"office-docs/manifest.json": `{"name":"office-docs","version":"` + version + `"}`,
		})
		pkg, err := store.Import("office-docs.zip", data, now)
		if err != nil {
			t.Fatalf("导入 %s 失败：%v", version, err)
		}
		if _, err := store.Publish(pkg.ID, now); err != nil {
			t.Fatalf("发布 %s 失败：%v", version, err)
		}
		return pkg
	}
	first := mk("1.0.0")
	second := mk("1.1.0")

	all, err := store.List()
	if err != nil {
		t.Fatalf("列表失败：%v", err)
	}
	published := map[string]string{}
	for _, p := range all {
		if p.Status == skillStatusPublished {
			published[p.Version] = p.Name
		}
	}
	if len(published) != 1 || published["1.1.0"] != "office-docs" {
		t.Fatalf("发布新版本后应只剩一个已发布版本，实际 %v", published)
	}
	_ = first
	_ = second

	// 导出包里也只应有一份。
	blob, _, err := store.Bundle(now)
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	count := 0
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/SKILL.md") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("导出包应只有 1 个 SKILL.md，实际 %d", count)
	}
}

func TestSkillBundleManifestMatchesRecords(t *testing.T) {
	store, _ := newSkillTestStore(t)
	now := time.Unix(1790000000, 0)
	data := buildZip(t, map[string]string{
		"office-docs/SKILL.md":      goodSkillMD,
		"office-docs/manifest.json": `{"name":"office-docs","version":"2.0.0"}`,
	})
	pkg, err := store.Import("x.zip", data, now)
	if err != nil {
		t.Fatalf("导入失败：%v", err)
	}
	if _, err := store.Publish(pkg.ID, now); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	blob, _, err := store.Bundle(now)
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	var manifest struct {
		Skills []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			SHA256  string `json:"sha256"`
		} `json:"skills"`
	}
	for _, f := range zr.File {
		if f.Name != ".manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("读 manifest 失败：%v", err)
		}
		if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
			t.Fatalf("manifest 解析失败：%v", err)
		}
		rc.Close()
	}
	if len(manifest.Skills) != 1 || manifest.Skills[0].Name != "office-docs" ||
		manifest.Skills[0].Version != "2.0.0" || manifest.Skills[0].SHA256 == "" {
		t.Fatalf("manifest 内容不对：%+v", manifest)
	}
}
