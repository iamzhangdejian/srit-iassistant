package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
)

// legacyFixture 读取 testdata 里的真实 OLE2 样本。
//
// 样本来自 richardlehane/mscfb 的测试集：test.doc 1.1MB 太大，改用
// novpapplan.doc（117KB），PowerPoint 用 test.ppt（79KB）。两者都是真正的
// Word 97-2003 / PowerPoint 97-2003 二进制，能覆盖 anydoc 的 OLE2 解析路径。
func legacyFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "legacy", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func legacyConvertRequest(t *testing.T, fileName string, data []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents/convert", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = req
	return ctx, recorder
}

// TestConvertLegacyWordToMarkdown 覆盖 .doc → Markdown 的主路径。
//
// 断言的是「转换成功且拿到正文」，不锁死具体文字：anydoc 的段落切分随版本
// 变化，锁字符串会让测试变成版本探针。
func TestConvertLegacyWordToMarkdown(t *testing.T) {
	if !anydoc.Available() {
		t.Skip("anydoc binding not built into this test binary")
	}
	ctx, recorder := legacyConvertRequest(t, "方案说明.doc", legacyFixture(t, "sample.doc"))
	NewDocumentConvertHandler().ConvertLegacyPreview(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
			Format   string `json:"format"`
			Engine   string `json:"engine"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.Success || payload.Data.Format != "doc" || payload.Data.Engine != "anydoc" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if len(strings.TrimSpace(payload.Data.Markdown)) < 20 {
		t.Fatalf("markdown too short: %q", payload.Data.Markdown)
	}
}

// TestConvertLegacyPowerPointToMarkdown 覆盖 .ppt → Markdown。
func TestConvertLegacyPowerPointToMarkdown(t *testing.T) {
	if !anydoc.Available() {
		t.Skip("anydoc binding not built into this test binary")
	}
	ctx, recorder := legacyConvertRequest(t, "汇报.ppt", legacyFixture(t, "sample.ppt"))
	NewDocumentConvertHandler().ConvertLegacyPreview(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"format":"ppt"`) {
		t.Fatalf("expected ppt format, body = %s", recorder.Body.String())
	}
}

// TestConvertRejectsModernOffice 说明白名单只放旧格式：docx 由浏览器渲染，
// 走服务端转换只会多一次往返。
func TestConvertRejectsModernOffice(t *testing.T) {
	ctx, recorder := legacyConvertRequest(t, "报告.docx", []byte("PK\x03\x04 not really a docx"))
	NewDocumentConvertHandler().ConvertLegacyPreview(ctx)

	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "conversion_type_unsupported") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

// TestConvertRejectsMissingFile 覆盖 multipart 里没有 file 字段的情况。
func TestConvertRejectsMissingFile(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("file_name", "x.doc")
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents/convert", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = req

	NewDocumentConvertHandler().ConvertLegacyPreview(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
