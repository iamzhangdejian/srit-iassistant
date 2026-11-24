package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterProjectRoutes 注册「项目」相关路由。
//
// 项目是 工作空间 → 项目 → 会话 三层结构的中间层：一个项目绑定一个用户
// 经系统目录选择框授权过的本地目录，项目下的会话在创建时继承该目录。
//
// 权限：项目属于项目级配置的载体（决定会话能在哪个本地目录工作），
// 因此全部接口要求 Viewer 及以上；创建/改名/删除额外要求对知识库配置的
// 管理权限，与「模型管理」「存储引擎」这类工作空间级设置保持一致，
// 避免只读成员随意把别人的目录绑定成项目。
func RegisterProjectRoutes(
	r *gin.RouterGroup,
	handler *handler.ProjectHandler,
	g *rbacGuards,
) {
	// 读取：任何能进入本工作空间的成员都可以看到项目树。
	// 用 apiKeyFullAccess 而不是新增能力位：项目是会话的组织方式，
	// 与 chat 能力同等粒度，没必要引入新的 API Key capability。
	projects := g.apiKeyGroup(r.Group("/projects", g.Viewer()), apiKeyChat(apiKeyFullAccess()))
	{
		projects.GET("", handler.ListProjects)
		projects.GET("/:id", handler.GetProject)
	}

	// 写入：绑定本地目录会扩大沙箱可访问的路径集合，属于管理性操作。
	writable := g.apiKeyGroup(r.Group("/projects", g.Admin()), apiKeyChat(apiKeyFullAccess()))
	{
		writable.POST("", handler.CreateProject)
		writable.PUT("/:id", handler.UpdateProject)
		writable.PUT("/:id/default", handler.SetDefaultProject)
		writable.DELETE("/:id", handler.DeleteProject)
	}
}