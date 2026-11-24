package skilltree

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// InstallFromDir 把 srcDir 的整棵目录装成 name 的一个新版本并切过去。
//
// 与 NewVersion + 手工拷贝相比，这里多做两件事：
//   - 拷贝失败时丢弃半成品版本目录，避免下次 NewVersion 复用它；
//   - 安装成功后 Prune 掉同名的其它版本，因为调用方（启动时装入内置技能）
//     只在「这个名字还没装过」时才会走到这里，历史版本没有回滚价值。
//
// 返回新版本目录的绝对路径。
func (t *Tree) InstallFromDir(name, srcDir string) (string, error) {
	if err := validName(name); err != nil {
		return "", err
	}
	info, err := os.Stat(filepath.Join(srcDir, "SKILL.md"))
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("skilltree: %q 里没有 SKILL.md", srcDir)
	}
	versionDir, err := t.NewVersion(name)
	if err != nil {
		return "", err
	}
	if err := copyTree(srcDir, versionDir); err != nil {
		_ = os.RemoveAll(versionDir)
		return "", err
	}
	if _, err := t.Activate(name, versionDir); err != nil {
		_ = os.RemoveAll(versionDir)
		return "", err
	}
	if err := t.Prune(name, versionDir); err != nil {
		// 清理旧版本失败不影响这次安装：技能已经可用，残留目录由 Sweep 负责。
		return versionDir, nil
	}
	return versionDir, nil
}

// copyTree 递归复制 src 到 dst（dst 已存在）。符号链接直接跳过：
// 内置技能包由我们自己的打包脚本产出，出现链接属于异常，不该顺着它去读本机别的文件。
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// BundledNames 列出目录下看起来像技能的子目录名（含 SKILL.md）。
//
// 启动时用它决定装哪些：打包脚本只负责把技能摊进 Resources/skills，
// 「哪些是真技能」由这里判断，避免把 README 之类的东西装进去。
func BundledNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md"))
		if err != nil || info.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	return names, nil
}
