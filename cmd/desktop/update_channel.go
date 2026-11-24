package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/mod/semver"
)

// 自有更新通道（阶段 4）。
//
// 为什么不用 GitHub：客户现场是内网，GitHub 既不可达也不该成为依赖；
// 发布节奏、版本说明、灰度撤回都应由管理端自己决定。所以默认走
// `WK_UPDATE_BASE_URL`（管理端地址），GitHub 通道保留给开发自测。
//
// 检查逻辑刻意做成「一次 GET + 字符串比较」：更新检查失败绝不能影响
// 正常使用，所有错误路径都只记日志。

// updateBaseURL 由打包脚本用 -ldflags "-X main.updateBaseURL=..." 注入。
var updateBaseURL string

const (
	// updateBaseURLEnv 允许运维临时指向另一个地址，无需重新打包。
	updateBaseURLEnv = "WK_UPDATE_BASE_URL"
	// updateChannelEnv 允许在 stable / beta 之间切换。
	updateChannelEnv = "WK_UPDATE_CHANNEL"
	// updateDefaultChannel 是发布通道缺省值。
	updateDefaultChannel = "stable"
)

// resolveUpdateBaseURL 返回生效的更新服务地址（不带结尾斜杠）。
func resolveUpdateBaseURL() string {
	base := strings.TrimSpace(os.Getenv(updateBaseURLEnv))
	if base == "" {
		base = strings.TrimSpace(updateBaseURL)
	}
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/")
}

// resolveUpdateChannel 返回生效的发布通道。
func resolveUpdateChannel() string {
	ch := strings.ToLower(strings.TrimSpace(os.Getenv(updateChannelEnv)))
	if ch == "beta" {
		return "beta"
	}
	return updateDefaultChannel
}

// updateLatestResponse 对应管理端 GET /api/updates/latest 的响应。
type updateLatestResponse struct {
	UpdateAvailable bool   `json:"update_available"`
	Current         string `json:"current"`
	LatestVersion   string `json:"latest_version"`
	Reason          string `json:"reason"`
	Latest          *struct {
		Version     string `json:"version"`
		Notes       string `json:"notes"`
		SizeBytes   int64  `json:"size_bytes"`
		SHA256      string `json:"sha256"`
		PublishedAt int64  `json:"published_at"`
		URL         string `json:"url"`
	} `json:"latest"`
}

// updateLatestURL 拼出检查更新的请求地址。
//
// current 允许为空（首次安装没有版本号时，直接拉最新包）。
func updateLatestURL(base, platform, arch, channel, current string) string {
	q := url.Values{}
	q.Set("platform", platform)
	q.Set("arch", arch)
	q.Set("channel", channel)
	if current != "" {
		q.Set("current", current)
	}
	return base + "/api/updates/latest?" + q.Encode()
}

// absoluteUpdateURL 把服务端返回的相对下载地址补成绝对地址。
func absoluteUpdateURL(base, path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(base, "/") + path
}

// fetchUpdateLatest 查询更新服务并解析结果。
//
// 单独抽出来是为了能测：真正的检查函数要弹原生对话框，测试里跑不起来。
func fetchUpdateLatest(ctx context.Context, base, platform, arch, channel, current string) (*updateLatestResponse, error) {
	target := updateLatestURL(base, platform, arch, channel, current)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GuoYan-Desktop-App/"+current)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("更新服务返回状态码 %d", resp.StatusCode)
	}

	var latest updateLatestResponse
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		return nil, fmt.Errorf("解析更新信息失败：%w", err)
	}
	return &latest, nil
}

// checkChannelUpdate 走自有通道检查更新。
//
// 与 GitHub 通道的区别：不弹「无法连接」错误框——内网离线是常态，
// 只有用户主动点「检查更新」时才提示失败，自动检查静默跳过。
func checkChannelUpdate(ctx context.Context, base, currentVersion string, showUpToDate, autoDownload bool) {
	channel := resolveUpdateChannel()
	current := strings.TrimPrefix(currentVersion, "v")

	latest, err := fetchUpdateLatest(ctx, base, runtime.GOOS, runtime.GOARCH, channel, current)
	if err != nil {
		logger.Warnf(context.Background(), "检查更新失败（%s）：%v", base, err)
		if showUpToDate {
			wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
				Type:          wailsruntime.ErrorDialog,
				Title:         "检查更新失败",
				Message:       fmt.Sprintf("无法连接更新服务：\n%s\n\n%v", base, err),
				Buttons:       []string{"确定"},
				DefaultButton: "确定",
			})
		}
		return
	}

	if !latest.UpdateAvailable || latest.Latest == nil {
		logger.Infof(context.Background(), "已是最新版本：当前 %s，通道 %s（%s）",
			currentVersion, channel, latest.Reason)
		if showUpToDate {
			msg := fmt.Sprintf("当前已是最新版本。\n\n当前版本：%s", currentVersion)
			if latest.Reason != "" {
				msg = fmt.Sprintf("%s\n%s", latest.Reason, msg)
			}
			wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
				Type:          wailsruntime.InfoDialog,
				Title:         "已是最新版本",
				Message:       msg,
				Buttons:       []string{"确定"},
				DefaultButton: "确定",
			})
		}
		return
	}

	latestVersion := latest.Latest.Version
	// 保底再比一次：服务端已判断过，但客户端多一道校验可以避免
	// 「服务端认为是新版、本地 semver 认为是旧版」这种脏数据把用户绕进去。
	if semver.IsValid("v"+latestVersion) && semver.IsValid("v"+current) {
		if semver.Compare("v"+latestVersion, "v"+current) <= 0 {
			logger.Warnf(context.Background(),
				"服务端提示有新版本 %s，但本地比较未见更新（当前 %s），忽略", latestVersion, currentVersion)
			return
		}
	}

	downloadURL := absoluteUpdateURL(base, latest.Latest.URL)
	logger.Infof(context.Background(),
		"发现新版本 %s（当前 %s，%s，sha256=%s），下载地址 %s",
		latestVersion, currentVersion, channel, shortcutSHA(latest.Latest.SHA256), downloadURL)

	if autoDownload && downloadURL != "" {
		downloadAndInstall(ctx, downloadURL, releaseFileName(downloadURL, latestVersion),
			currentVersion, latestVersion, true)
		return
	}

	msg := fmt.Sprintf("发现新版本 %s。\n\n当前版本：%s\n最新版本：%s", latestVersion, currentVersion, latestVersion)
	if latest.Latest.Notes != "" {
		msg += "\n\n更新说明：\n" + strings.TrimSpace(latest.Latest.Notes)
	}
	msg += "\n\n是否立即下载？"

	choice, _ := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
		Type:          wailsruntime.InfoDialog,
		Title:         "发现新版本",
		Message:       msg,
		Buttons:       []string{"下载", "取消"},
		DefaultButton: "下载",
	})
	if choice == "下载" && downloadURL != "" {
		downloadAndInstall(ctx, downloadURL, releaseFileName(downloadURL, latestVersion),
			currentVersion, latestVersion, false)
	}
}

// releaseFileName 从下载地址里取文件名；取不到就按平台与版本拼一个，
// 免得下载到临时目录时一堆文件同名互相覆盖。
func releaseFileName(downloadURL, version string) string {
	if u, err := url.Parse(downloadURL); err == nil {
		base := u.Path[strings.LastIndex(u.Path, "/")+1:]
		if base != "" && strings.Contains(base, ".") && base != "download" {
			return base
		}
	}
	ext := ".zip"
	if runtime.GOOS == "windows" {
		ext = ".exe"
	} else if runtime.GOOS == "linux" {
		ext = ".AppImage"
	}
	return fmt.Sprintf("国研智研体-%s-%s%s", strings.TrimPrefix(version, "v"), runtime.GOOS, ext)
}

// shortcutSHA 只取前 12 位打日志：完整 sha256 在日志里既没可读性也不必要。
func shortcutSHA(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
