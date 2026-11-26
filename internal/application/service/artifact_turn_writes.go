// Package service - per-turn written-file registry.
//
// Why this file exists
// --------------------
// On the desktop "host" backend the sandbox workspace IS the user's project
// directory, so WorkspaceLayout.OutputDir == WorkspaceLayout.Root. The
// artifact collector refuses to scan such a layout (scanning Root would upload
// node_modules, .git and the user's whole source tree), which also means files
// the assistant legitimately produced during the turn never reach the
// artifacts panel.
//
// The fix is not to scan Root but to record exactly which paths the turn's
// tools wrote, and collect only those. This registry is that record: tool
// implementations call NoteTurnWrite whenever they create or modify a file, and
// ArtifactCollector collects that set instead of walking a directory.
//
// The registry is keyed by session and is bounded so a long-lived process
// cannot accumulate entries for sessions nobody will collect again.
//
// Layering note: internal/agent/tools cannot import this package (service
// already imports tools), so the tools reach the registry through the hook
// exported by internal/agent/tools and wired in container.NewContainer.
package service

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// maxTrackedSessions bounds the registry. A desktop install has a handful of
// concurrent sessions; this only exists so a leak cannot grow without limit.
const maxTrackedSessions = 256

// maxWritesPerTurn bounds one turn's recorded paths. A script that writes
// thousands of files should not turn into thousands of uploads.
const maxWritesPerTurn = 200

// turnWriteRegistry records the absolute paths written during a turn.
//
// It is intentionally a process-wide singleton rather than a context value:
// the write happens inside a tool, the collect happens in the stream handler
// after the agent finishes, and threading a collector through every tool
// constructor would touch a dozen files for no benefit. The session id is the
// only correlation key either side needs.
type turnWriteRegistry struct {
	mu       sync.Mutex
	sessions map[string]map[string]struct{}
	order    []string
}

var turnWrites = &turnWriteRegistry{sessions: make(map[string]map[string]struct{})}

// NoteTurnWrite records that this turn created or modified path.
//
// path must be absolute. Relative paths, empty paths and directories are
// ignored: the collector needs a concrete file to read later.
func NoteTurnWrite(sessionID, path string) {
	if sessionID == "" || path == "" {
		return
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return
	}
	turnWrites.note(sessionID, clean)
}

func (r *turnWriteRegistry) note(sessionID, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	paths, ok := r.sessions[sessionID]
	if !ok {
		if len(r.sessions) >= maxTrackedSessions {
			// Drop the oldest session wholesale. Its turn is over; a collect
			// that arrives later simply finds nothing and degrades to "no
			// artifacts", which is the same as the pre-feature behaviour.
			if len(r.order) > 0 {
				delete(r.sessions, r.order[0])
				r.order = r.order[1:]
			}
		}
		paths = make(map[string]struct{})
		r.sessions[sessionID] = paths
		r.order = append(r.order, sessionID)
	}
	if len(paths) >= maxWritesPerTurn {
		return
	}
	paths[path] = struct{}{}
}

// TakeTurnWrites returns the paths recorded for sessionID and forgets them,
// so the next turn starts from an empty set.
func TakeTurnWrites(sessionID string) []string {
	return turnWrites.take(sessionID)
}

func (r *turnWriteRegistry) take(sessionID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	paths, ok := r.sessions[sessionID]
	if !ok {
		return nil
	}
	delete(r.sessions, sessionID)
	for i, id := range r.order {
		if id == sessionID {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	return out
}

// ResetTurnWrites drops whatever was recorded for sessionID without returning
// it. The stream handler calls this before a turn starts so a previous turn's
// leftovers cannot leak into this one.
func ResetTurnWrites(sessionID string) {
	_ = turnWrites.take(sessionID)
}

// PickWrittenEntries narrows a directory listing to the files this turn wrote.
//
// Entries come from ListSessionFiles, whose Path is absolute on the host
// backend and remote-absolute on remote backends. A recorded write may name the
// file either way, so matching compares cleaned paths first, then falls back to
// the basename for a recorded path that lives outside the listing root (the
// model cannot write outside the workspace through PathGuard, but a skill
// script can touch a temp file elsewhere).
func PickWrittenEntries(entries []sandbox.RemoteDirEntry, written []string) []sandbox.RemoteDirEntry {
	if len(entries) == 0 || len(written) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(written))
	wantNames := make(map[string]struct{}, len(written))
	for _, p := range written {
		clean := filepath.Clean(p)
		want[clean] = struct{}{}
		wantNames[filepath.Base(clean)] = struct{}{}
	}

	matched := make(map[string]struct{}, len(entries))
	out := make([]sandbox.RemoteDirEntry, 0, len(entries))
	add := func(e sandbox.RemoteDirEntry) {
		if _, dup := matched[e.Path]; dup {
			return
		}
		matched[e.Path] = struct{}{}
		out = append(out, e)
	}

	for _, e := range entries {
		clean := filepath.Clean(e.Path)
		if _, ok := want[clean]; ok {
			add(e)
			continue
		}
		// A recorded path may sit above the listing root (the skill advertised
		// the project root while the write landed in a subdirectory) or below
		// it; match on the anchored suffix in both directions.
		sep := string(filepath.Separator)
		for w := range want {
			if strings.HasSuffix(clean, sep+w) || strings.HasPrefix(w, clean+sep) {
				add(e)
				break
			}
		}
		if _, dup := matched[e.Path]; dup {
			continue
		}
		// Basename is the last resort: it can over-match when two directories
		// hold a file with the same name, but missing the artifact entirely is
		// the worse failure for the user.
		if _, ok := wantNames[filepath.Base(clean)]; ok {
			add(e)
		}
	}
	return out
}