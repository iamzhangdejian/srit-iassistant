//go:build !bindings

package main

// 内置技能安装。
//
// 打包脚本会把管理端导出的发布包解到 <resources>/skills/<name>/，
// 这里在启动时把还没装过的技能装进本地技能树（~/.weknora/skills）。
//
// 为什么不直接由安装包铺到 ~/.weknora/skills：安装目录属于用户数据，
// 升级时不该被安装包直接改写；由客户端按版本树安装，才能保留版本目录、
// 出错回滚，以及「用户自己改过就跳过」的语义。

import (
	"context"
	"path/filepath"

	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/localsandbox/skilltree"
	"github.com/Tencent/WeKnora/internal/logger"
)

// installBundledSkills 把资源目录里的技能装进本地技能树，返回本次新装的数量。
func installBundledSkills(ctx context.Context, resourcesDir string, hasResources bool) int {
	if !hasResources || resourcesDir == "" {
		return 0
	}
	root, err := container.HostSkillsRoot()
	if err != nil {
		logger.Warnf(ctx, "[skills] 解析本地技能目录失败：%v", err)
		return 0
	}
	return installSkillsInto(ctx, bundledSkillsDir(resourcesDir), root)
}

// installSkillsInto 是真正干活的版本：源目录与技能树根都由调用方给，
// 便于在没有桌面环境的机器上测。
func installSkillsInto(ctx context.Context, srcDir, skillsRoot string) int {
	names, err := skilltree.BundledNames(srcDir)
	if err != nil {
		logger.Warnf(ctx, "[skills] 读取内置技能目录失败：%v", err)
		return 0
	}
	if len(names) == 0 {
		return 0
	}
	tree, err := skilltree.New(skillsRoot)
	if err != nil {
		logger.Warnf(ctx, "[skills] 打开本地技能树失败：%v", err)
		return 0
	}
	installed := 0
	for _, name := range names {
		// 已装过的同名技能直接跳过：用户可能改过其中的脚本，
		// 启动时覆盖等于悄悄回滚他的修改。
		if tree.Installed(name) {
			continue
		}
		if _, err := tree.InstallFromDir(name, filepath.Join(srcDir, name)); err != nil {
			logger.Warnf(ctx, "[skills] 安装内置技能 %s 失败：%v", name, err)
			continue
		}
		installed++
	}
	if installed > 0 {
		logger.Infof(ctx, "[skills] 已安装 %d 个内置技能到 %s", installed, skillsRoot)
	}
	return installed
}

// bundledSkillsDir 返回资源目录下的技能目录。
func bundledSkillsDir(resourcesDir string) string {
	if resourcesDir == "" {
		return ""
	}
	return filepath.Join(resourcesDir, "skills")
}
