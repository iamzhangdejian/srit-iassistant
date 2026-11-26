package session

import (
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/hostdir"
)

// HostProjectDirsLoader returns the user-approved project directories from
// desktop prefs. Nil means no directory is approved.
//
// Aliased to hostdir.Loader so existing wiring in cmd/desktop and container
// keeps compiling unchanged; the implementation lives in internal/hostdir to
// avoid an import cycle with the service layer.
type HostProjectDirsLoader = hostdir.Loader

func (h *Handler) approvedDirs() []string {
	if h == nil || h.approvedProjectDirs == nil {
		return nil
	}
	return h.approvedProjectDirs()
}

// MatchApprovedDir reports whether raw names a directory the user approved
// through the native picker, returning it cleaned.
//
// Kept as a thin forwarder because it is referenced from container and tests;
// the rule itself (exact match after Clean, children not honoured) lives in
// hostdir.MatchApproved.
func MatchApprovedDir(raw string, approved []string) (string, bool) {
	return hostdir.MatchApproved(raw, approved)
}

// bindHostWorkspaceDir accepts an omitted/empty project_dir as unbound. A
// non-empty value must be an absolute path already on the approved list
// (compared after Clean). The stored value is the cleaned path.
func bindHostWorkspaceDir(raw string, approved []string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	return MatchApprovedDir(raw, approved)
}

// cleanedAbsDir is a small helper kept for readability at call sites that only
// need the canonical form of an already-approved path.
func cleanedAbsDir(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		return ""
	}
	return filepath.Clean(raw)
}