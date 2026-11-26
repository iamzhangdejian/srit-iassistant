//go:build desktop

package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newShellJobService wires Lite shell jobs. Remote sandboxes are hidden, so
// the index stays nil. A nil stream manager stays a nil emitter pointer:
// storing (*BackgroundEventEmitter)(nil) in the lifecycle event interface
// panics on the first call. The keep-aliver matches the web wiring;
// NewBackgroundKeepAliver is a no-op for a host sandbox, and a nil keeper
// fails the remote submit path.
func newShellJobService(
	_ *redis.Client,
	pinned *service.PinnedSessionSandbox,
	bg *service.BackgroundWork,
	streams interfaces.StreamManager,
	cleaner interfaces.ResourceCleaner,
	host service.HostSandboxManager,
) (*service.ShellJobService, error) {
	var events *service.BackgroundEventEmitter
	if streams != nil {
		events = service.NewBackgroundEventEmitter(streams)
	}
	jobs := service.NewShellJobService(pinned, bg, nil, events, service.NewBackgroundKeepAliver(pinned))
	if bg != nil {
		bg.Handler = jobs
	}
	provider, ok := host.Manager.(interface{ LocalSandboxService() *localsandbox.Service })
	if !ok || provider.LocalSandboxService() == nil {
		return jobs, nil
	}
	// 便携模式与桌面默认路径二选一：WK_PORTABLE_DIR 下所有可写状态集中存放，
	// 家目录不可写的环境（沙箱、企业管控）也能正常启动。
	var jobsDir string
	if portable := strings.TrimSpace(os.Getenv("WK_PORTABLE_DIR")); portable != "" {
		jobsDir = filepath.Join(portable, "jobs")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		jobsDir = filepath.Join(home, ".weknora", "jobs")
	}
	local, err := service.NewLocalShellJobService(
		provider.LocalSandboxService(), bg, events, jobsDir,
	)
	if err != nil {
		return nil, err
	}
	jobs.WithLocal(local)
	cleaner.RegisterWithName("LocalShellJobs", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return local.Shutdown(ctx)
	})
	return jobs, nil
}
