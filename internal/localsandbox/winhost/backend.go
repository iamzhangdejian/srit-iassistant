//go:build windows

// Package winhost is the Windows sandbox backend. The intended isolation is a
// dedicated hidden account, a restricted token, per-root capability SIDs,
// firewall rules, and a job object for the process tree. This build reports
// Windows as unavailable rather than running unsandboxed.
package winhost

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/localsandbox/core"
	"github.com/Tencent/WeKnora/internal/logger"
)

// ErrFilesOnlyScope is the user-facing explanation of what a Windows build can
// and cannot do. container.buildHostSandbox turns a backend error into a nil
// manager, which clears settings.sandbox.host: the composer then hides the
// sandbox panel and the shell tool is never offered, so nothing runs unsandboxed.
const (
	ErrFilesOnlyScope = "windows host sandbox is not implemented: shell execution and " +
		"the artifacts it would produce stay unavailable on Windows"
)

// New reports Windows as unavailable rather than running unsandboxed.
//
// Deliberate degradation, not a bug: the alternative (running commands with the
// user's own token) would be worse than missing the feature. Files still work —
// knowledge Q&A, projects, project directory approval, preview and download all
// go through the HTTP layer and need no sandbox.
func New() (core.Backend, error) {
	err := fmt.Errorf("%w: %s", core.ErrUnsupportedPlatform, ErrFilesOnlyScope)
	// Warn, not Error: this is the documented state of the Windows build, and an
	// Error on every start would page operators for expected behaviour.
	logger.Warnf(context.Background(),
		"[LocalSandbox] %v — knowledge Q&A, projects and file preview remain available", err)
	return nil, err
}
