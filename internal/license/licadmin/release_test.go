package licadmin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newReleaseTestStore(t *testing.T) *ReleaseStore {
	t.Helper()
	auth, err := OpenAuthStore(t.TempDir() + "/licadmin.db")
	if err != nil {
		t.Fatalf("打开测试库失败：%v", err)
	}
	t.Cleanup(func() { _ = auth.Close() })
	store, err := NewReleaseStoreForData(auth.DB(), t.TempDir())
	if err != nil {
		t.Fatalf("建发布库失败：%v", err)
	}
	return store
}

func importRelease(t *testing.T, store *ReleaseStore, version string) Release {
	t.Helper()
	rel, err := store.Import(Release{
		Platform: "darwin",
		Arch:     "arm64",
		Version:  version,
		Filename: "国研智研体-" + version + ".zip",
	}, []byte("fake-package-"+version), time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("导入 %s 失败：%v", version, err)
	}
	return rel
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.9.0", "0.10.0", -1},
		{"0.10.0", "0.9.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.0-beta.1", 1}, // 正式版大于预发布
		{"1.0.0-beta", "1.0.0", -1},  // 反过来
		{"1.0", "1.0.0", 0},          // 缺段按 0 处理
		{"1.2.3.4", "1.2.3.3", 1},    // 第四段参与比较
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q,%q)=%d，期望 %d", c.a, c.b, got, c.want)
		}
	}
}

func TestReleaseImportAndPublishKeepsSinglePublished(t *testing.T) {
	store := newReleaseTestStore(t)
	old := importRelease(t, store, "0.9.0")
	newer := importRelease(t, store, "0.9.1")

	if old.Status != releaseStatusDraft {
		t.Fatalf("新上传应为草稿，实际 %s", old.Status)
	}
	if _, err := store.Publish(old.ID, time.Now()); err != nil {
		t.Fatalf("发布旧版失败：%v", err)
	}
	if _, err := store.Publish(newer.ID, time.Now()); err != nil {
		t.Fatalf("发布新版失败：%v", err)
	}

	// 同平台/架构/通道只能有一个已发布版本。
	gotOld, _ := store.Get(old.ID)
	if gotOld.Status != releaseStatusDraft {
		t.Fatalf("旧版应被退回草稿，实际 %s", gotOld.Status)
	}
	latest, err := store.Latest("darwin", "arm64", "stable")
	if err != nil {
		t.Fatalf("取最新版本失败：%v", err)
	}
	if latest.Version != "0.9.1" {
		t.Fatalf("最新版本应为 0.9.1，实际 %s", latest.Version)
	}
}

func TestReleaseLatestIgnoresDraftAndOtherPlatforms(t *testing.T) {
	store := newReleaseTestStore(t)
	draft := importRelease(t, store, "1.0.0")

	// 只有草稿时应该「没有已发布版本」，而不是把草稿当最新。
	if _, err := store.Latest("darwin", "arm64", "stable"); err != ErrReleaseNotFound {
		t.Fatalf("只有草稿时应返回 ErrReleaseNotFound，实际 %v", err)
	}

	if _, err := store.Publish(draft.ID, time.Now()); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if _, err := store.Latest("windows", "amd64", "stable"); err != ErrReleaseNotFound {
		t.Fatal("其它平台不该取到 darwin 的包")
	}
	if _, err := store.Latest("darwin", "arm64", "stable"); err != nil {
		t.Fatalf("同平台应取到，实际 %v", err)
	}
}

func TestReleaseRejectsInvalidMetadata(t *testing.T) {
	store := newReleaseTestStore(t)
	bad := []Release{
		{Platform: "solaris", Arch: "arm64", Version: "1.0.0", Filename: "a.zip"},
		{Platform: "darwin", Arch: "mips", Version: "1.0.0", Filename: "a.zip"},
		{Platform: "darwin", Arch: "arm64", Channel: "nightly", Version: "1.0.0", Filename: "a.zip"},
		{Platform: "darwin", Arch: "arm64", Version: "", Filename: "a.zip"},
		{Platform: "darwin", Arch: "arm64", Version: "abc", Filename: "a.zip"},
	}
	for i, r := range bad {
		if _, err := store.Import(r, []byte("x"), time.Now()); err == nil {
			t.Errorf("第 %d 条非法元数据应被拒绝", i)
		}
	}
}

func TestReleaseDuplicateRejectedAndFileCleaned(t *testing.T) {
	store := newReleaseTestStore(t)
	importRelease(t, store, "0.9.0")
	if _, err := store.Import(Release{Platform: "darwin", Arch: "arm64", Version: "0.9.0",
		Filename: "重复包.zip"}, []byte("dup"), time.Now()); err != ErrReleaseExists {
		t.Fatalf("重复版本应返回 ErrReleaseExists，实际 %v", err)
	}
}

func TestReleaseDeleteRequiresUnpublish(t *testing.T) {
	store := newReleaseTestStore(t)
	rel := importRelease(t, store, "0.9.0")
	if _, err := store.Publish(rel.ID, time.Now()); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if err := store.Delete(rel.ID); err != ErrReleasePublished {
		t.Fatalf("已发布版本不可删除，实际 %v", err)
	}
	if _, err := store.Unpublish(rel.ID); err != nil {
		t.Fatalf("撤回失败：%v", err)
	}
	if err := store.Delete(rel.ID); err != nil {
		t.Fatalf("撤回后应可删除，实际 %v", err)
	}
}

func TestUpdateLatestEndpoint(t *testing.T) {
	store := newReleaseTestStore(t)
	rel := importRelease(t, store, "0.9.1")
	if _, err := store.Publish(rel.ID, time.Now()); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	srv := NewServer(ServerOptions{Releases: store})

	// 老版本应拿到更新 + 下载地址与 sha256。
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/updates/latest?platform=darwin&arch=arm64&current=0.9.0", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"update_available":true`, `"latest_version":"0.9.1"`, `"sha256"`, "/api/updates/download/"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("响应缺少 %s：%s", want, body)
		}
	}

	// 已是最新：update_available 为 false，且不带 latest 块。
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/updates/latest?platform=darwin&arch=arm64&current=0.9.1", nil))
	if body := rec.Body.String(); !bytes.Contains([]byte(body), []byte(`"update_available":false`)) {
		t.Errorf("同版本不应提示更新：%s", body)
	}

	// 没有已发布版本的平台：200 + 明确原因，客户端据此提示「未配置」。
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/updates/latest?platform=windows&arch=amd64&current=0.9.0", nil))
	if body := rec.Body.String(); !bytes.Contains([]byte(body), []byte("还没有已发布版本")) {
		t.Errorf("应说明该平台未发布：%s", body)
	}
}

func TestUpdateDownloadOnlyServesPublished(t *testing.T) {
	store := newReleaseTestStore(t)
	draft := importRelease(t, store, "0.9.2")
	srv := NewServer(ServerOptions{Releases: store})

	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/updates/download/"+itoa(draft.ID), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("草稿不该被下载，实际状态码 %d", rec.Code)
	}

	if _, err := store.Publish(draft.ID, time.Now()); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/updates/download/"+itoa(draft.ID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("已发布应可下载，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("X-Release-Version"); got != "0.9.2" {
		t.Errorf("下载响应应带版本头，实际 %q", got)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
