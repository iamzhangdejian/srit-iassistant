package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterDocumentConvertRoutes 注册「文档转换预览」路由。
//
// 只有一个接口：把浏览器打不开的旧版 Office 二进制（.doc/.ppt/.xls）转成
// Markdown 供预览面板渲染。转换在本进程内完成（anydoc 绑定的 Rust 库），
// 不依赖 docreader 服务，因此桌面单机形态也能用。
//
// 权限：Viewer 即可。转换只读客户端上传的字节、不落盘、不接触任何工作空间
// 数据，与「预览自己已经能下载的文件」同等粒度。
func RegisterDocumentConvertRoutes(
	r *gin.RouterGroup,
	handler *handler.DocumentConvertHandler,
	g *rbacGuards,
) {
	group := r.Group("/documents", g.Viewer())
	{
		group.POST("/convert", handler.ConvertLegacyPreview)
	}
}