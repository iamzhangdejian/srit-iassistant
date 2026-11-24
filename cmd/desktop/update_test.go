package main

import "testing"

// 更新源解析必须「环境变量优先、留空即关闭」。
// 这条不变量防的是：某次打包忘了注入 updateRepo，却意外回落到某个默认仓库。
func TestResolveUpdateRepo(t *testing.T) {
	orig := updateRepo
	t.Cleanup(func() { updateRepo = orig })

	updateRepo = "srit/knowledge-workbench"
	t.Setenv(updateRepoEnv, "")
	if got := resolveUpdateRepo(); got != "srit/knowledge-workbench" {
		t.Errorf("ldflags value: got %q", got)
	}

	t.Setenv(updateRepoEnv, "  mirror/kb  ")
	if got := resolveUpdateRepo(); got != "mirror/kb" {
		t.Errorf("env override: got %q", got)
	}

	updateRepo = ""
	t.Setenv(updateRepoEnv, "   ")
	if got := resolveUpdateRepo(); got != "" {
		t.Errorf("blank inputs must disable updates, got %q", got)
	}
}
