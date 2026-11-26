package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// listedOnlySource is a source without metadata lookup: it can only walk a
// directory, which is what the collector falls back to.
type listedOnlySource struct {
	entries []sandbox.RemoteDirEntry
}

func (s *listedOnlySource) ListSessionFiles(
	context.Context, string, string,
) ([]sandbox.RemoteDirEntry, error) {
	return s.entries, nil
}

func (s *listedOnlySource) ReadSessionFile(context.Context, string, string) ([]byte, error) {
	return []byte("data"), nil
}

// statSource adds StatSessionFile, like every sandbox.SessionFileStore does.
type statSource struct {
	listedOnlySource
	files map[string]*sandbox.RemoteStatEntry
	err   error
}

func (s *statSource) StatSessionFile(
	_ context.Context, _, path string,
) (*sandbox.RemoteStatEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	entry, ok := s.files[filepath.Clean(path)]
	if !ok {
		return nil, errors.New("not found")
	}
	return entry, nil
}

// The recursive listing is capped (maxListSessionFiles). A file written into a
// large project therefore sits past the cap and never matched, which is how a
// generated PDF stayed out of the artifacts panel: the listing showed 500
// entries and none of them was the deliverable.
func TestResolveWrittenEntriesStatsPastListingCap(t *testing.T) {
	root := "/tmp/proj"
	entries := make([]sandbox.RemoteDirEntry, 0, 600)
	for i := 0; i < 600; i++ {
		entries = append(entries, sandbox.RemoteDirEntry{
			Name: fmt.Sprintf("f%d.md", i), Path: filepath.Join(root, fmt.Sprintf("f%d.md", i)),
			Type: sandbox.RemoteEntryFile,
		})
	}
	target := filepath.Join(root, "out", "report.pdf")
	mod := time.Unix(1700000000, 0)
	source := &statSource{
		listedOnlySource: listedOnlySource{entries: entries},
		files: map[string]*sandbox.RemoteStatEntry{
			target: {Path: target, Type: sandbox.RemoteEntryFile, Size: 2048, ModTime: mod},
		},
	}

	got := resolveWrittenEntries(context.Background(), source, "s1", root, []string{target})

	require.Len(t, got, 1)
	require.Equal(t, target, got[0].Path)
	require.EqualValues(t, 2048, got[0].Size)
	require.Equal(t, mod, got[0].ModTime)
}

// Sources without metadata lookup keep the old behaviour.
func TestResolveWrittenEntriesFallsBackToListing(t *testing.T) {
	root := "/tmp/proj"
	target := filepath.Join(root, "report.pdf")
	source := &listedOnlySource{entries: []sandbox.RemoteDirEntry{
		{Name: "report.pdf", Path: target, Type: sandbox.RemoteEntryFile},
	}}

	got := resolveWrittenEntries(context.Background(), source, "s1", root, []string{target})

	require.Len(t, got, 1)
	require.Equal(t, target, got[0].Path)
}

// A stat failure must not swallow the file when the listing can still find it.
func TestResolveWrittenEntriesFallsBackWhenStatFails(t *testing.T) {
	root := "/tmp/proj"
	target := filepath.Join(root, "report.pdf")
	source := &statSource{
		listedOnlySource: listedOnlySource{entries: []sandbox.RemoteDirEntry{
			{Name: "report.pdf", Path: target, Type: sandbox.RemoteEntryFile},
		}},
		err: errors.New("no live sandbox"),
	}

	got := resolveWrittenEntries(context.Background(), source, "s1", root, []string{target})

	require.Len(t, got, 1)
	require.Equal(t, target, got[0].Path)
}

// Paths recorded outside the workspace root are not session artifacts.
func TestResolveWrittenEntriesIgnoresPathsOutsideRoot(t *testing.T) {
	root := "/tmp/proj"
	outside := "/tmp/elsewhere/secret.txt"
	source := &statSource{
		listedOnlySource: listedOnlySource{},
		files: map[string]*sandbox.RemoteStatEntry{
			outside: {Path: outside, Type: sandbox.RemoteEntryFile, Size: 10},
		},
	}

	got := resolveWrittenEntries(context.Background(), source, "s1", root, []string{outside})

	require.Empty(t, got)
}

// A directory reported by stat is not collectable.
func TestResolveWrittenEntriesSkipsDirectories(t *testing.T) {
	root := "/tmp/proj"
	dir := filepath.Join(root, "out")
	source := &statSource{
		listedOnlySource: listedOnlySource{},
		files: map[string]*sandbox.RemoteStatEntry{
			dir: {Path: dir, Type: sandbox.RemoteEntryDir},
		},
	}

	got := resolveWrittenEntries(context.Background(), source, "s1", root, []string{dir})

	require.Empty(t, got)
}
