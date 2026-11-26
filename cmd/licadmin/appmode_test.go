package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestIsAppBundlePath(t *testing.T) {
	cases := []struct {
		exe  string
		want bool
	}{
		{"/Users/x/dist_install/国研智研体授权管理.app/Contents/MacOS/licadmin", true},
		{"/tmp/t.app/Contents/MacOS/licadmin", true},
		{"/Users/x/weknora_src/bin/licadmin", false},
		{"/usr/local/bin/licadmin", false},
		{"/tmp/t.app/Contents/Resources/licadmin", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isAppBundlePath(c.exe); got != c.want {
			t.Fatalf("isAppBundlePath(%q) = %v, want %v", c.exe, got, c.want)
		}
	}
}

// TestProbeExistingInstance 覆盖「双击第二次」的判定。
//
// 这个判定错了的后果是二选一的坏结果：误判「已在运行」→ 用户的窗口明明没开
// 却打不开；漏判 → 两个进程同时写同一个 licadmin.db，恢复包和备份互相打架。
func TestProbeExistingInstance(t *testing.T) {
	ours := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><head><title>" + instanceMarker + "</title></head></html>"))
	}))
	defer ours.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><head><title>别的服务</title></head></html>"))
	}))
	defer other.Close()

	cases := []struct {
		name string
		addr string
		want bool
	}{
		{"本机已有授权管理端", ours.URL, true},
		{"端口上是别的服务", other.URL, false},
		{"端口没人监听", "http://127.0.0.1:1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, err := url.Parse(c.addr)
			if err != nil {
				t.Fatalf("解析地址失败：%v", err)
			}
			host, portStr, err := net.SplitHostPort(u.Host)
			if err != nil {
				t.Fatalf("拆分地址失败：%v", err)
			}
			port, _ := strconv.Atoi(portStr)
			if got := probeExistingInstance(host, port); got != c.want {
				t.Fatalf("probeExistingInstance(%q) = %v，期望 %v", c.addr, got, c.want)
			}
		})
	}
}
