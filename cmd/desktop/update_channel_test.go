package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateLatestURLCarriesPlatformAndVersion(t *testing.T) {
	got := updateLatestURL("http://127.0.0.1:8091", "darwin", "arm64", "stable", "0.9.0")
	for _, want := range []string{
		"http://127.0.0.1:8091/api/updates/latest?",
		"platform=darwin", "arch=arm64", "channel=stable", "current=0.9.0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("URL 缺少 %q：%s", want, got)
		}
	}

	// 没有版本号（首次安装）时不带 current，服务端会直接给最新包。
	if got := updateLatestURL("http://x", "darwin", "arm64", "stable", ""); strings.Contains(got, "current=") {
		t.Fatalf("空版本不该带 current：%s", got)
	}
}

func TestAbsoluteUpdateURL(t *testing.T) {
	cases := []struct{ base, path, want string }{
		{"http://127.0.0.1:8091", "/api/updates/download/3", "http://127.0.0.1:8091/api/updates/download/3"},
		{"http://127.0.0.1:8091/", "/api/updates/download/3", "http://127.0.0.1:8091/api/updates/download/3"},
		{"http://127.0.0.1:8091", "api/updates/download/3", "http://127.0.0.1:8091/api/updates/download/3"},
		{"http://127.0.0.1:8091", "https://cdn.example.com/a.zip", "https://cdn.example.com/a.zip"},
		{"http://127.0.0.1:8091", "", ""},
	}
	for _, c := range cases {
		if got := absoluteUpdateURL(c.base, c.path); got != c.want {
			t.Errorf("absoluteUpdateURL(%q,%q)=%q，期望 %q", c.base, c.path, got, c.want)
		}
	}
}

func TestResolveUpdateChannel(t *testing.T) {
	t.Setenv(updateChannelEnv, "")
	if got := resolveUpdateChannel(); got != "stable" {
		t.Fatalf("默认应是 stable，实际 %q", got)
	}
	t.Setenv(updateChannelEnv, "BETA")
	if got := resolveUpdateChannel(); got != "beta" {
		t.Fatalf("BETA 应归一化为 beta，实际 %q", got)
	}
	t.Setenv(updateChannelEnv, "nightly")
	if got := resolveUpdateChannel(); got != "stable" {
		t.Fatalf("非法通道应回落 stable，实际 %q", got)
	}
}

func TestResolveUpdateBaseURLTrimsSlash(t *testing.T) {
	t.Setenv(updateBaseURLEnv, "http://192.168.1.9:8091/")
	if got := resolveUpdateBaseURL(); got != "http://192.168.1.9:8091" {
		t.Fatalf("结尾斜杠应被去掉，实际 %q", got)
	}
	t.Setenv(updateBaseURLEnv, "   ")
	old := updateBaseURL
	updateBaseURL = "http://build-time:8091"
	t.Cleanup(func() { updateBaseURL = old })
	if got := resolveUpdateBaseURL(); got != "http://build-time:8091" {
		t.Fatalf("环境变量为空时应回落到编译期注入值，实际 %q", got)
	}
}

func TestReleaseFileNameFromURL(t *testing.T) {
	got := releaseFileName("http://127.0.0.1:8091/api/updates/download/3", "0.9.0")
	if !strings.HasPrefix(got, "国研智研体-0.9.0-") {
		t.Fatalf("未知地址时应按平台与版本拼文件名，实际 %q", got)
	}
	if got := releaseFileName("http://x/files/国研智研体-0.9.1.zip", "0.9.1"); got != "国研智研体-0.9.1.zip" {
		t.Fatalf("应从地址里取真实文件名，实际 %q", got)
	}
}

func TestShortcutSHA(t *testing.T) {
	if got := shortcutSHA("abcdef0123456789"); got != "abcdef012345" {
		t.Fatalf("应只保留前 12 位，实际 %q", got)
	}
	if got := shortcutSHA("short"); got != "short" {
		t.Fatalf("短串原样返回，实际 %q", got)
	}
}

func TestFetchUpdateLatestParsesAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/updates/latest" {
			t.Fatalf("请求路径不对：%s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("platform") != "darwin" || q.Get("arch") != "arm64" || q.Get("channel") != "stable" {
			t.Fatalf("查询参数不对：%s", r.URL.RawQuery)
		}
		if q.Get("current") != "0.9.0" {
			t.Fatalf("应带当前版本，实际 %q", q.Get("current"))
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "GuoYan-Desktop-App/") {
			t.Fatalf("User-Agent 不对：%q", ua)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"update_available":true,"current":"0.9.0","latest_version":"0.9.1",
			"latest":{"version":"0.9.1","notes":"修复折叠侧栏","size_bytes":123,"sha256":"abc","published_at":1795416008,"url":"/api/updates/download/2"}}`))
	}))
	defer srv.Close()

	got, err := fetchUpdateLatest(context.Background(), srv.URL, "darwin", "arm64", "stable", "0.9.0")
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	if !got.UpdateAvailable || got.Latest == nil || got.Latest.Version != "0.9.1" {
		t.Fatalf("解析结果不对：%+v", got)
	}
	if abs := absoluteUpdateURL(srv.URL, got.Latest.URL); abs != srv.URL+"/api/updates/download/2" {
		t.Fatalf("下载地址拼接不对：%s", abs)
	}
}

func TestFetchUpdateLatestNoReleaseYet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"current":"0.9.0","reason":"该平台/架构还没有已发布版本","update_available":false}`))
	}))
	defer srv.Close()

	got, err := fetchUpdateLatest(context.Background(), srv.URL, "windows", "amd64", "stable", "0.9.0")
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	if got.UpdateAvailable || got.Latest != nil {
		t.Fatalf("不该报有更新：%+v", got)
	}
	if got.Reason == "" {
		t.Fatal("应把服务端原因带回来，供界面区分「未配置」与「已最新」")
	}
}

func TestFetchUpdateLatestSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := fetchUpdateLatest(context.Background(), srv.URL, "darwin", "arm64", "stable", "0.9.0"); err == nil {
		t.Fatal("非 200 应返回错误，让调用方决定是否提示")
	}
}
