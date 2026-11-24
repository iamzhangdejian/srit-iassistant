package container

import (
	"os"
	"path/filepath"
)

// 本地技能树路径。
//
// 这里不带构建标签是刻意的：cmd/desktop 的启动逻辑（cmd/desktop/skills_boot.go）
// 要在任何平台都能编译通过，而 sandbox_host.go 只在 desktop 标签下编入。
// 路径计算本身与沙箱实现无关，放在这里两处共用同一个常量表达式，
// 避免「安装到 A 目录、沙箱读 B 目录」这类只在运行时才暴露的问题。

// DefaultHostSkillsRoot 返回 homeDir 下的本地技能树根目录。
func DefaultHostSkillsRoot(homeDir string) string {
	return filepath.Join(homeDir, ".weknora", "skills")
}

// HostSkillsRoot 返回当前用户本地技能树根目录。
func HostSkillsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return DefaultHostSkillsRoot(home), nil
}
