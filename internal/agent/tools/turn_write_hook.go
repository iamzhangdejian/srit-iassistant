// Package tools — turn-write hook.
//
// Some backends (the desktop "host" sandbox) run the workspace directly inside
// the user's project directory. There the artifact collector must not walk a
// directory to find deliverables: walking the project root would upload the
// user's whole source tree. Instead the file-writing tools report every path
// they created or modified, and the collector collects exactly those paths.
//
// The hook lives here rather than in the service package because
// internal/application/service already imports internal/agent/tools; importing
// back would be a cycle. container.NewContainer wires the service-side registry
// into this variable at startup, and every hook call is nil-safe so a tools
// package used in isolation (unit tests, the CLI) keeps working.
package tools

import (
	"context"
	"path"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// turnWriteHook records one written path for a session. Wired at startup.
var turnWriteHook atomic.Value // func(sessionID, absPath string)

// WriteObserver receives absolute paths written by the file tools.
type WriteObserver func(sessionID, absolutePath string)

// SetTurnWriteObserver installs the observer. Passing nil disables reporting.
func SetTurnWriteObserver(observer WriteObserver) {
	if observer == nil {
		turnWriteHook = atomic.Value{}
		return
	}
	turnWriteHook.Store(observer)
}

// noteTurnWrite reports one written path. Errors are impossible: an unused
// hook means nothing is watching, which is the remote-backend case.
func noteTurnWrite(sessionID, absolutePath string) {
	if sessionID == "" || absolutePath == "" {
		return
	}
	v := turnWriteHook.Load()
	if v == nil {
		return
	}
	observer, ok := v.(WriteObserver)
	if !ok || observer == nil {
		return
	}
	observer(sessionID, absolutePath)
}

// shellWriteMu serialises the "command ran, now diff the tree" step so two
// concurrent shell_exec calls in the same session cannot attribute each
// other's files.
var shellWriteMu sync.Mutex

// resolveObservedPath turns a sandbox-relative path into the absolute path the
// session workspace expects. Tool writes are already absolute on the host
// backend, so this is only a guard against a relative path slipping through.
func resolveObservedPath(rootDir, p string) string {
	if rootDir == "" || p == "" {
		return p
	}
	if path.IsAbs(p) {
		return p
	}
	return path.Join(rootDir, p)
}

// hostWriteSkew tolerates filesystem timestamp granularity: a file written
// right after a command starts may carry an mtime a beat earlier than the
// recorded start instant.
const hostWriteSkew = 2 * time.Second

// hostWriteScanner is the host backend's mtime-bounded scan. Only the local
// adapter implements it; any other executor falls back to filtering a listing.
type hostWriteScanner interface {
	FilesWrittenSince(ctx context.Context, sessionID, dir string, since time.Time) ([]sandbox.RemoteDirEntry, error)
}

// noteHostShellWrites reports the files a shell command just produced.
//
// The file tools call noteTurnWrite themselves, but a command can create any
// number of files (a script that renders a PDF, a pandoc run, a build step)
// and nothing else in the pipeline knows they exist. Snapshotting the project
// before and after used to answer this, but the listing is capped at 500
// entries, so in any real project the deliverable was silently dropped and the
// artifacts panel stayed empty while the file sat in the project directory.
// Scanning by modification time is bounded by the command, not by the tree.
func noteHostShellWrites(
	ctx context.Context, sessionID, root string, started time.Time, executor SandboxCommandExecutor,
) {
	if sessionID == "" || root == "" || started.IsZero() || turnWriteHook.Load() == nil {
		return
	}
	// Serialised so two concurrent commands in one session cannot attribute
	// each other's files.
	shellWriteMu.Lock()
	defer shellWriteMu.Unlock()

	since := started.Add(-hostWriteSkew)
	var entries []sandbox.RemoteDirEntry
	if scanner, ok := executor.(hostWriteScanner); ok {
		scanned, err := scanner.FilesWrittenSince(ctx, sessionID, root, since)
		if err != nil {
			logger.Warnf(ctx, "[Tool][ShellExec] host write scan failed session=%s root=%s: %v",
				sessionID, root, err)
			return
		}
		entries = scanned
	} else {
		// Executors without the scan capability (tests, install mode): list and
		// filter here. Same truncation caveat, but these are not the desktop path.
		listed, ok := sandboxOutputSnapshot(ctx, executor, sessionID, root)
		if !ok {
			return
		}
		for _, e := range listed {
			if !e.ModTime.Before(since) {
				entries = append(entries, e)
			}
		}
	}
	// Newest first: the registry keeps only the most recent writes per turn, and
	// the deliverable the user is waiting for is the last file the command wrote.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].ModTime.After(entries[j].ModTime)
	})
	for _, e := range entries {
		noteTurnWrite(sessionID, e.Path)
	}
	if len(entries) > 0 {
		logger.Infof(ctx, "[Tool][ShellExec] host writes session=%s root=%s files=%d",
			sessionID, root, len(entries))
	}
}
