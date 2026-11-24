package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hostLayout describes a session whose workspace is the user's own project
// directory — the desktop backend, where OutputDir is the root itself.
func hostProjectLayout(root string) sandbox.WorkspaceLayout {
	return sandbox.WorkspaceLayout{
		Origin:     sandbox.WorkspaceOriginHost,
		Root:       root,
		WriteRoots: []string{root},
		ReadRoots:  []string{root},
		Hint:       root,
	}
}

// scanningHostExecutor is a host backend whose adapter can scan by mtime.
type scanningHostExecutor struct {
	fakeShellExecutor
	layout  sandbox.WorkspaceLayout
	files   []sandbox.RemoteDirEntry
	scanned bool
	since   time.Time
	dir     string
}

func (e *scanningHostExecutor) SessionWorkspaceLayout(context.Context, string) (sandbox.WorkspaceLayout, error) {
	return e.layout, nil
}

func (e *scanningHostExecutor) FilesWrittenSince(
	_ context.Context, _, dir string, since time.Time,
) ([]sandbox.RemoteDirEntry, error) {
	e.scanned, e.since, e.dir = true, since, dir
	return e.files, nil
}

// listingHostExecutor is a host backend with no mtime scan: only the listing
// capability, which the fallback filters by modification time.
type listingHostExecutor struct {
	fakeShellExecutor
	layout  sandbox.WorkspaceLayout
	entries []sandbox.RemoteDirEntry
}

func (e *listingHostExecutor) SessionWorkspaceLayout(context.Context, string) (sandbox.WorkspaceLayout, error) {
	return e.layout, nil
}

func (e *listingHostExecutor) ListSessionFiles(context.Context, string, string) ([]sandbox.RemoteDirEntry, error) {
	return e.entries, nil
}

func captureTurnWrites(t *testing.T) *[]string {
	t.Helper()
	var got []string
	SetTurnWriteObserver(func(_, absolutePath string) { got = append(got, absolutePath) })
	t.Cleanup(func() { SetTurnWriteObserver(nil) })
	return &got
}

// A shell command's output is not known in advance, so the host backend has to
// discover it after the fact. This is the PDF case: the file is rendered by a
// script, exists in the project, and used to never reach the artifacts panel.
func TestShellExecReportsHostWritesFromTheScan(t *testing.T) {
	root := t.TempDir()
	pdf := filepath.Join(root, "报告.pdf")
	executor := &scanningHostExecutor{
		layout: hostProjectLayout(root),
		files: []sandbox.RemoteDirEntry{
			{Name: "报告.pdf", Path: pdf, Type: sandbox.RemoteEntryFile},
		},
	}
	executor.result = &sandbox.ExecuteResult{ExitCode: 0}
	writes := captureTurnWrites(t)

	tool := NewShellExecTool(executor, nil)
	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"pandoc -o 报告.pdf in.md"}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.True(t, executor.scanned, "the host scan must run after the command")
	assert.Equal(t, root, executor.dir)
	assert.Equal(t, []string{pdf}, *writes)
	// The window must start at (or just before) the command, never after it.
	assert.False(t, executor.since.After(time.Now()))
	assert.True(t, executor.since.Before(time.Now().Add(time.Minute)))
}

// Executors without the scan capability still report writes, by filtering the
// listing they do have on modification time.
func TestShellExecReportsHostWritesWithoutScanCapability(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "旧文件.md")
	fresh := filepath.Join(root, "新文件.pdf")
	executor := &listingHostExecutor{
		layout: hostProjectLayout(root),
		entries: []sandbox.RemoteDirEntry{
			{Name: "旧文件.md", Path: old, Type: sandbox.RemoteEntryFile, ModTime: time.Now().Add(-time.Hour)},
			{Name: "新文件.pdf", Path: fresh, Type: sandbox.RemoteEntryFile, ModTime: time.Now()},
		},
	}
	executor.result = &sandbox.ExecuteResult{ExitCode: 0}
	writes := captureTurnWrites(t)

	tool := NewShellExecTool(executor, nil)
	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(`{"command":"echo hi"}`))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, []string{fresh}, *writes, "a pre-existing file must not be claimed as this turn's output")
}

// A failed command changes nothing the user asked for; it must not report the
// files it did touch, or retrying a broken conversion would accumulate junk.
func TestShellExecSkipsHostWritesWhenCommandFails(t *testing.T) {
	root := t.TempDir()
	executor := &scanningHostExecutor{
		layout: hostProjectLayout(root),
		files: []sandbox.RemoteDirEntry{
			{Name: "half.pdf", Path: filepath.Join(root, "half.pdf"), Type: sandbox.RemoteEntryFile},
		},
	}
	executor.err = context.DeadlineExceeded
	writes := captureTurnWrites(t)

	tool := NewShellExecTool(executor, nil)
	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(`{"command":"sleep 999"}`))

	require.NoError(t, err)
	require.False(t, result.Success)
	assert.False(t, executor.scanned)
	assert.Empty(t, *writes)
}
