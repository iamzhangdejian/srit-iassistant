// Package hostdir owns the approved host-directory boundary shared by the
// session handler, the project service and the Lite sandbox.
//
// It exists as its own package to break an import cycle: the project service
// needs to validate a directory, and the session handler imports the service
// layer, so the validation cannot live in either of them.
//
// The rule it encodes is the security boundary for host filesystem access: a
// path is usable only when it exactly equals (after filepath.Clean) a directory
// the user authorised through the native folder picker.
package hostdir

import (
	"path/filepath"
	"strings"
)

// Loader returns the user-approved project directories from desktop prefs.
// Nil means no directory is approved. Applies to Lite desktop; a plain server
// leaves it nil and therefore allows nothing.
type Loader func() []string

// MatchApproved reports whether raw names a directory the user approved,
// returning it cleaned.
//
// Matching is exact after Clean. A subdirectory of an approved directory is
// NOT approved: the user picked a root, and honouring children would let a
// stored value reach deeper than the picker ever offered.
func MatchApproved(raw string, approved []string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		return "", false
	}
	cleaned := filepath.Clean(raw)
	for _, dir := range approved {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if filepath.Clean(dir) == cleaned {
			return cleaned, true
		}
	}
	return "", false
}

// MatchApprovedWithLoader is the convenience form used by services that keep a
// Loader rather than a materialised slice.
func MatchApprovedWithLoader(raw string, loader Loader) (string, bool) {
	if loader == nil {
		return MatchApproved(raw, nil)
	}
	return MatchApproved(raw, loader())
}