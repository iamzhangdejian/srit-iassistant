package handler

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/gin-gonic/gin"
)

// DocumentConvertHandler converts documents the browser cannot open on its own
// into Markdown the preview pane can render.
//
// Only the legacy OLE2 office formats are routed here: `.doc`, `.ppt` and
// `.xls`. Their modern OOXML siblings (`docx`, `pptx`, `xlsx`) render in the
// browser already, so sending them through a server round trip would only add
// latency. anydoc also handles OpenDocument, RTF and EPUB, but the frontend
// previewers cover those clientside or fall back to download, and widening the
// endpoint would widen the attack surface for no visible gain.
type DocumentConvertHandler struct{}

// NewDocumentConvertHandler builds the handler. It holds no state: the parser
// is a package-level binding.
func NewDocumentConvertHandler() *DocumentConvertHandler {
	return &DocumentConvertHandler{}
}

// legacyPreviewExtensions is the allow list. Extensions are checked here after
// anydoc reports the file type, so a request cannot widen the surface by
// claiming a supported extension for arbitrary bytes: anydoc still has to
// recognize the container.
var legacyPreviewExtensions = map[string]struct{}{
	"doc": {},
	"dot": {},
	"ppt": {},
	"pot": {},
	"xls": {},
}

// maxLegacyConvertBytes caps the upload. The desktop app previews project files
// locally and never needs more for a slide deck or a word-processing document;
// the limit also keeps one conversion from pinning a CPU core for minutes.
const maxLegacyConvertBytes = 64 << 20

// ConvertLegacyPreview accepts a multipart upload ("file") and answers with
// GitHub-Flavored Markdown plus the engine that produced it.
//
// Status semantics matter to the client: 415 means "not a document we convert",
// 413 means "too large to convert in memory", 501 means this binary was built
// without the anydoc binding, and 422 means anydoc refused the content. The
// preview pane shows the message and offers a download in every case.
func (h *DocumentConvertHandler) ConvertLegacyPreview(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "conversion_file_missing", "message": "缺少待转换的文件"},
		})
		return
	}
	name := fileHeader.Filename
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	format, supported := anydoc.FormatForFile(ext, name)
	if _, allowed := legacyPreviewExtensions[ext]; !allowed || !supported {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "conversion_type_unsupported",
				"message": "该格式不支持转换预览：" + ext,
			},
		})
		return
	}
	if fileHeader.Size > maxLegacyConvertBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "conversion_too_large",
				"message": "文件超过 64MB，无法转换预览，请下载后查看",
			},
		})
		return
	}
	if !anydoc.Available() {
		c.JSON(http.StatusNotImplemented, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "conversion_engine_unavailable",
				"message": "当前安装包没有内置文档转换引擎（" + anydoc.UnavailableReason() + "）",
			},
		})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("读取上传文件失败"))
		return
	}
	defer file.Close()
	// Read one byte past the limit so a lying Content-Length cannot slip a
	// larger document through: the extra byte turns into a 413 below.
	data, err := io.ReadAll(io.LimitReader(file, maxLegacyConvertBytes+1))
	if err != nil {
		_ = c.Error(apperrors.NewInternalServerError("读取上传文件失败"))
		return
	}
	if len(data) > maxLegacyConvertBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "conversion_too_large",
				"message": "文件超过 64MB，无法转换预览，请下载后查看",
			},
		})
		return
	}
	if len(data) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "conversion_file_empty", "message": "文件内容为空"},
		})
		return
	}

	result, err := anydoc.Convert(data, anydoc.Options{Format: format})
	switch {
	case err == nil:
	case errors.Is(err, anydoc.ErrUnavailable):
		c.JSON(http.StatusNotImplemented, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "conversion_engine_unavailable",
				"message": "当前安装包没有内置文档转换引擎",
			},
		})
		return
	case errors.Is(err, anydoc.ErrResourceLimit):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "conversion_too_complex",
				"message": "文档结构过于复杂，无法转换预览，请下载后查看",
			},
		})
		return
	default:
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "conversion_failed",
				"message": "文档转换失败：" + err.Error(),
			},
		})
		return
	}

	markdown := strings.TrimSpace(result.Markdown)
	if markdown == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "conversion_empty",
				"message": "文档转换后没有可显示的内容，请下载后查看",
			},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"markdown": markdown,
			"format":   format,
			"engine":   "anydoc",
		},
	})
}