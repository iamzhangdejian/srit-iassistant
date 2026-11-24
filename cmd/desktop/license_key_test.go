//go:build !bindings

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadBundledPublicKeyFindsMacAppResources(t *testing.T) {
	app := filepath.Join(t.TempDir(), "国研智研体.app")
	resources := filepath.Join(app, "Contents", "Resources")
	require.NoError(t, os.MkdirAll(resources, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(resources, "public.key"), []byte(" mac-key \n"), 0o644))

	exe := filepath.Join(app, "Contents", "MacOS", "国研智研体")
	require.Equal(t, "mac-key", readBundledPublicKeyFrom(exe))
}

func TestReadBundledPublicKeyFindsSiblingKeyOnWindowsAndLinux(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "public.key"), []byte("win-key\n"), 0o644))

	require.Equal(t, "win-key", readBundledPublicKeyFrom(filepath.Join(dir, "国研智研体.exe")))
	require.Equal(t, "win-key", readBundledPublicKeyFrom(filepath.Join(dir, "国研智研体")))
}

func TestReadBundledPublicKeyFallsBackToTheResourceRoot(t *testing.T) {
	// AppImage：二进制在 usr/bin，资源根在 usr/share/<app> 且启动时已 chdir 过去
	appDir := t.TempDir()
	resources := filepath.Join(appDir, "usr", "share", "国研智研体")
	require.NoError(t, os.MkdirAll(resources, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(resources, "public.key"), []byte("appimage-key\n"), 0o644))
	t.Chdir(resources)

	exe := filepath.Join(appDir, "usr", "bin", "国研智研体")
	require.Equal(t, "appimage-key", readBundledPublicKeyFrom(exe))
}

func TestReadBundledPublicKeyReturnsEmptyWhenNothingIsBundled(t *testing.T) {
	t.Chdir(t.TempDir())
	require.Empty(t, readBundledPublicKeyFrom(filepath.Join(t.TempDir(), "bin", "app")))
}
