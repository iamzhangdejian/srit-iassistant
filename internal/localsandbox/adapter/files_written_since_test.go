package adapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The shell tool cannot know what a command writes, so it asks the host
// adapter for everything touched since the command started. This is the only
// path by which a script-produced deliverable (a rendered PDF, for instance)
// reaches the artifacts panel, and it must work in projects larger than the
// listing cap.
func TestFilesWrittenSinceReportsOnlyFreshFiles(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	ctx := context.Background()
	layout, err := a.SessionWorkspaceLayout(ctx, "s1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(layout.Root, 0o755))

	stale := filepath.Join(layout.Root, "existing.md")
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o644))
	longAgo := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(stale, longAgo, longAgo))

	since := time.Now()
	fresh := filepath.Join(layout.Root, "报告.pdf")
	require.NoError(t, os.WriteFile(fresh, []byte("%PDF-1.4"), 0o644))

	entries, err := a.FilesWrittenSince(ctx, "s1", ".", since)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, fresh, entries[0].Path)
	require.Equal(t, int64(8), entries[0].Size)
}

// Dependency trees and VCS objects are never deliverables, and walking them
// would dominate the scan on a real project.
func TestFilesWrittenSinceSkipsHeavyDirectories(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	ctx := context.Background()
	layout, err := a.SessionWorkspaceLayout(ctx, "s1")
	require.NoError(t, err)

	since := time.Now().Add(-time.Second)
	for _, dir := range []string{"node_modules/pkg", ".git/objects"} {
		full := filepath.Join(layout.Root, dir)
		require.NoError(t, os.MkdirAll(full, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(full, "noise.js"), []byte("x"), 0o644))
	}
	require.NoError(t, os.MkdirAll(layout.Root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(layout.Root, "keep.md"), []byte("x"), 0o644))

	entries, err := a.FilesWrittenSince(ctx, "s1", ".", since)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "keep.md", entries[0].Name)
}

// The whole point over the listing-based diff: a project with more files than
// maxListSessionFiles must still yield the file the command just wrote.
func TestFilesWrittenSinceIsNotLimitedByTheListingCap(t *testing.T) {
	a := New(testService(t, &stubBackend{}))
	ctx := context.Background()
	layout, err := a.SessionWorkspaceLayout(ctx, "s1")
	require.NoError(t, err)

	bulk := filepath.Join(layout.Root, "bulk")
	require.NoError(t, os.MkdirAll(bulk, 0o755))
	longAgo := time.Now().Add(-time.Hour)
	for i := 0; i < maxListSessionFiles+40; i++ {
		p := filepath.Join(bulk, fmt.Sprintf("old-%04d.md", i))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		require.NoError(t, os.Chtimes(p, longAgo, longAgo))
	}
	since := time.Now()
	deliverable := filepath.Join(layout.Root, "结果.pdf")
	require.NoError(t, os.WriteFile(deliverable, []byte("%PDF"), 0o644))

	entries, err := a.FilesWrittenSince(ctx, "s1", ".", since)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, deliverable, entries[0].Path)
}
