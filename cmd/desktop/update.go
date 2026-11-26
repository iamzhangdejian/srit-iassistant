package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/logger"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/mod/semver"
)

// updateRepo 是自动更新拉取的 GitHub 仓库，格式 "owner/repo"。
//
// OEM 发行版绝不能指向上游 Tencent/WeKnora：那样「检查更新」会把用户
// 升级成未定制的上游版本，品牌、裁剪与授权全部丢失。因此这里默认空，
// 由打包方通过 -ldflags "-X main.updateRepo=<owner/repo>" 或运行期环境
// 变量 WK_UPDATE_REPO 指定。留空 = 不启用自动更新（明确提示，不静默）。
var updateRepo string

// updateRepoEnv 允许运维临时覆盖更新源（比如内网镜像），无需重新打包。
const updateRepoEnv = "WK_UPDATE_REPO"

// resolveUpdateRepo 返回生效的更新仓库，环境变量优先于编译期注入。
func resolveUpdateRepo() string {
	if v := strings.TrimSpace(os.Getenv(updateRepoEnv)); v != "" {
		return v
	}
	return strings.TrimSpace(updateRepo)
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Assets  []githubAsset `json:"assets"`
}

func checkUpdate(ctx context.Context, currentVersion string, showUpToDate bool, autoDownload bool) {
	go func() {
		if currentVersion == "unknown" || currentVersion == "" {
			if showUpToDate {
				wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.InfoDialog,
					Title:         "Check for Updates",
					Message:       "Unable to determine the current version. Cannot check for updates.",
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		if !strings.HasPrefix(currentVersion, "v") {
			currentVersion = "v" + currentVersion
		}

		// 自有通道优先：交付形态默认走管理端 /api/updates/latest，
		// 只有没配 base URL 时才回落到 GitHub（开发自测用）。
		if base := resolveUpdateBaseURL(); base != "" {
			checkChannelUpdate(ctx, base, strings.TrimPrefix(currentVersion, "v"), showUpToDate, autoDownload)
			return
		}

		repo := resolveUpdateRepo()
		if repo == "" {
			logger.Infof(context.Background(),
				"Auto update skipped: %s / -ldflags main.updateBaseURL 与 main.updateRepo 都没设置", updateBaseURLEnv)
			if showUpToDate {
				wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.InfoDialog,
					Title:         "Check for Updates",
					Message:       "这个安装包没有配置更新服务（WK_UPDATE_BASE_URL 未设置），请联系管理员获取新版本。",
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo), nil)
		if err != nil {
			logger.Warnf(context.Background(), "Check update failed: %v", err)
			return
		}

		// Add User-Agent header which is required/recommended by GitHub API
		req.Header.Set("User-Agent", "GuoYan-Desktop-App")

		// Add Authorization header if GITHUB_TOKEN is present to increase rate limit
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			logger.Warnf(context.Background(), "Check update failed: %v", err)
			if showUpToDate {
				wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Check Update Failed",
					Message:       fmt.Sprintf("Failed to connect to GitHub to check for updates:\n%v", err),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logger.Warnf(context.Background(), "GitHub API returned status code: %d", resp.StatusCode)
			if showUpToDate {
				msg := fmt.Sprintf("GitHub API returned an unexpected status code: %d", resp.StatusCode)
				if resp.StatusCode == 403 {
					msg = "GitHub API rate limit exceeded. Please try again later."
				}
				wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Check Update Failed",
					Message:       msg,
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		var release githubRelease
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			logger.Warnf(context.Background(), "Failed to parse release info: %v", err)
			return
		}

		latestVersion := release.TagName
		if !strings.HasPrefix(latestVersion, "v") {
			latestVersion = "v" + latestVersion
		}

		if semver.IsValid(latestVersion) && semver.IsValid(currentVersion) {
			if semver.Compare(latestVersion, currentVersion) > 0 {
				assetURL, assetName := findBestAsset(release.Assets, runtime.GOOS, runtime.GOARCH)

				if autoDownload && assetURL != "" {
					// Silent download in background
					downloadAndInstall(ctx, assetURL, assetName, currentVersion, latestVersion, true)
					return
				}

				msg := fmt.Sprintf("A new version of 国研智研体 is available!\n\nCurrent version: %s\nLatest version: %s\n\nWould you like to download it now?", currentVersion, latestVersion)
				choice, _ := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.InfoDialog,
					Title:         "Update Available",
					Message:       msg,
					Buttons:       []string{"Download", "Cancel"},
					DefaultButton: "Download",
				})
				if choice == "Download" {
					if assetURL != "" {
						downloadAndInstall(ctx, assetURL, assetName, currentVersion, latestVersion, false)
					} else {
						// Fallback to opening the release page if no specific asset is found
						wailsruntime.BrowserOpenURL(ctx, release.HTMLURL)
					}
				}
				return
			}
		}

		if showUpToDate {
			wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
				Type:          wailsruntime.InfoDialog,
				Title:         "Up to Date",
				Message:       fmt.Sprintf("You are using the latest version of 国研智研体.\n\nCurrent version: %s", currentVersion),
				Buttons:       []string{"OK"},
				DefaultButton: "OK",
			})
		}
	}()
}

func findBestAsset(assets []githubAsset, goos, goarch string) (string, string) {
	osKeyword := ""
	switch goos {
	case "darwin":
		osKeyword = "mac"
	case "windows":
		osKeyword = "win"
	case "linux":
		osKeyword = "linux"
	}

	archKeyword := ""
	switch goarch {
	case "amd64":
		archKeyword = "amd64"
	case "arm64":
		archKeyword = "arm64"
	}

	// 1. Try to match both OS and Arch
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, osKeyword) && (strings.Contains(name, archKeyword) || strings.Contains(name, "universal") || strings.Contains(name, "aarch64")) {
			return asset.BrowserDownloadURL, asset.Name
		}
	}

	// 2. Try to match OS only (e.g. universal binaries without arch in name)
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, osKeyword) {
			return asset.BrowserDownloadURL, asset.Name
		}
	}

	// 3. MacOS specific fallback (e.g. .dmg)
	if goos == "darwin" {
		for _, asset := range assets {
			if strings.HasSuffix(strings.ToLower(asset.Name), ".dmg") {
				return asset.BrowserDownloadURL, asset.Name
			}
		}
	}

	// 4. Windows specific fallback (e.g. .exe)
	if goos == "windows" {
		for _, asset := range assets {
			if strings.HasSuffix(strings.ToLower(asset.Name), ".exe") {
				return asset.BrowserDownloadURL, asset.Name
			}
		}
	}

	return "", ""
}

func downloadAndInstall(ctx context.Context, url string, filename string, currentVersion string, latestVersion string, silent bool) {
	tempDir := os.TempDir()
	savePath := filepath.Join(tempDir, filename)

	go func() {
		logger.Infof(context.Background(), "Starting download from %s to %s", url, savePath)
		resp, err := http.Get(url)
		if err != nil {
			logger.Warnf(context.Background(), "Download failed: %v", err)
			if !silent {
				wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Download Failed",
					Message:       fmt.Sprintf("Failed to download the update:\n%v", err),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logger.Warnf(context.Background(), "Download failed, server returned status: %d", resp.StatusCode)
			if !silent {
				wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Download Failed",
					Message:       fmt.Sprintf("Server returned status: %d", resp.StatusCode),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		out, err := os.Create(savePath)
		if err != nil {
			logger.Warnf(context.Background(), "Failed to create file for download: %v", err)
			if !silent {
				wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Save Failed",
					Message:       fmt.Sprintf("Failed to save the update file:\n%v", err),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		if err != nil {
			logger.Warnf(context.Background(), "Error occurred during download copying: %v", err)
			if !silent {
				wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
					Type:          wailsruntime.ErrorDialog,
					Title:         "Download Error",
					Message:       fmt.Sprintf("An error occurred while downloading:\n%v", err),
					Buttons:       []string{"OK"},
					DefaultButton: "OK",
				})
			}
			return
		}

		logger.Infof(context.Background(), "Download completed successfully: %s", savePath)

		// Prompt user to restart
		choice, _ := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
			Type:          wailsruntime.InfoDialog,
			Title:         "Update Ready",
			Message:       fmt.Sprintf("国研智研体 %s has been downloaded successfully.\n\nWould you like to restart and install the new version now?", latestVersion),
			Buttons:       []string{"Restart Now", "Later"},
			DefaultButton: "Restart Now",
		})

		if choice == "Restart Now" {
			applyUpdateAndRestart(ctx, savePath)
		}
	}()
}

// desktopAboutVersion 优先使用构建脚本注入的 handler.Version，否则尝试读取仓库根目录 VERSION（本地 wails dev 等未带 ldflags 时）。
func desktopAboutVersion() string {
	if v := strings.TrimSpace(handler.Version); v != "" && v != "unknown" {
		return v
	}
	for _, p := range []string{
		"VERSION",
		filepath.Join("..", "..", "VERSION"),
		filepath.Join("..", "..", "..", "VERSION"),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}
	}
	return "unknown"
}

// applyUpdateAndRestart applies the downloaded update and restarts the application
func applyUpdateAndRestart(ctx context.Context, savePath string) {
	if runtime.GOOS == "windows" {
		scriptPath := filepath.Join(os.TempDir(), "weknora_update.bat")
		execPath, err := os.Executable()
		if err != nil {
			logger.Warnf(context.Background(), "Failed to get executable path: %v", err)
			return
		}
		scriptContent := fmt.Sprintf(`@echo off
timeout /t 2 /nobreak
start /wait "" "%s" /S
start "" "%s"
del "%%~f0"
`, savePath, execPath)
		os.WriteFile(scriptPath, []byte(scriptContent), 0755)

		cmd := exec.Command("cmd.exe", "/C", "start", "/b", scriptPath)
		cmd.Start()
		wailsruntime.Quit(ctx)

	} else if runtime.GOOS == "darwin" {
		if strings.HasSuffix(strings.ToLower(savePath), ".dmg") {
			go func() {
				execPath, err := os.Executable()
				if err != nil {
					logger.Warnf(context.Background(), "Failed to get executable path: %v", err)
					exec.Command("open", savePath).Start()
					wailsruntime.Quit(ctx)
					return
				}

				appBundlePath := filepath.Dir(filepath.Dir(filepath.Dir(execPath)))
				if !strings.HasSuffix(appBundlePath, ".app") {
					exec.Command("open", savePath).Start()
					wailsruntime.Quit(ctx)
					return
				}

				mountPoint := filepath.Join(os.TempDir(), "GuoYanUpdateMount")
				os.MkdirAll(mountPoint, 0755)

				cmdMount := exec.Command("hdiutil", "attach", savePath, "-mountpoint", mountPoint, "-nobrowse", "-quiet")
				if err := cmdMount.Run(); err != nil {
					logger.Warnf(context.Background(), "Failed to mount dmg: %v", err)
					exec.Command("open", savePath).Start()
					wailsruntime.Quit(ctx)
					return
				}

				entries, err := os.ReadDir(mountPoint)
				if err != nil {
					exec.Command("hdiutil", "detach", mountPoint, "-force").Run()
					exec.Command("open", savePath).Start()
					wailsruntime.Quit(ctx)
					return
				}

				var newAppPath string
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".app") {
						newAppPath = filepath.Join(mountPoint, entry.Name())
						break
					}
				}

				if newAppPath == "" {
					exec.Command("hdiutil", "detach", mountPoint, "-force").Run()
					exec.Command("open", savePath).Start()
					wailsruntime.Quit(ctx)
					return
				}

				appDir := filepath.Dir(appBundlePath)
				scriptPath := filepath.Join(os.TempDir(), "weknora_update.sh")
				scriptContent := fmt.Sprintf(`#!/bin/bash
sleep 2
if ! (rm -rf "%s" && cp -a "%s" "%s"); then
    osascript -e "do shell script \"rm -rf \\\"%s\\\" && cp -a \\\"%s\\\" \\\"%s\\\"\" with administrator privileges"
fi
hdiutil detach "%s" -force
open "%s"
rm "$0"
`, appBundlePath, newAppPath, appDir, appBundlePath, newAppPath, appDir, mountPoint, appBundlePath)

				os.WriteFile(scriptPath, []byte(scriptContent), 0755)

				exec.Command("bash", scriptPath).Start()
				wailsruntime.Quit(ctx)
			}()
		} else {
			exec.Command("open", savePath).Start()
			wailsruntime.Quit(ctx)
		}
	} else {
		exec.Command("xdg-open", savePath).Start()
		wailsruntime.Quit(ctx)
	}
}
