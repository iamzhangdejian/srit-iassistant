package router

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/licenser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/mcpserver"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types/interfaces"

	_ "github.com/Tencent/WeKnora/docs" // swagger docs
)

// =============================================================================
// OEM 改造：功能裁剪开关（FeatCut）
// -----------------------------------------------------------------------------
// 私有化 OEM 版本只需「对话 / 知识库+Wiki / 模型管理 / 插件市场」四个模块，
// 以下能力全部关闭——既不在前端展示，也**不再注册后端路由**，
// 因此即便有人直接构造请求也无法访问（后端接口级关闭，不只是隐藏 UI）。
//
// 语义：常量名为 FeatCutX = true 表示「裁剪掉 X」，对应注册点写作
//
//	if !FeatCutX { RegisterXxx(...) }，即 true 时不注册路由。
//
// 如需恢复某个能力，把对应常量改成 false 并重新编译，无需改任何业务代码。
// =============================================================================
const (
	// FeatCutAgents 智能体管理接口（/api/v1/agents 的增删改查、共享、编排）。
	//
	// 注意：这是「界面入口」级裁剪，不控制对话本身走哪条管线。
	// 桌面版只有一个固定助手，界面上不暴露选择/管理智能体的入口，因此这里
	// 保持 true。对话仍然需要 Agent 管线（AgentQA）来获得工具调用与文件
	// 写入能力，那条路由由下面的 FeatCutAgentChat 单独控制。
	FeatCutAgents = true
	// FeatCutAgentChat Agent 对话管线（POST /api/v1/agent-chat/:session_id）。
	//
	// 放开的原因：快速问答（RAG 管线）是只读的，不具备任何文件写入工具，
	// 用户让它「生成 md 文档」只会得到一段正文，磁盘上什么也不会发生。
	// 要让「对话中由模型自行判断是否写文件」成立，必须走带工具调用的
	// Agent 链路。前端固定传一个内置 agent_id，界面上不再有选择入口。
	FeatCutAgentChat = false
	// FeatCutGraph 知识图谱检索
	FeatCutGraph = true
	// FeatCutDataSource 第三方数据源同步（含网页爬虫类数据源）
	FeatCutDataSource = true
	// FeatCutMCP MCP 服务 / MCP Server / MCP 端点
	FeatCutMCP = true
	// FeatCutBrowserSkill 浏览器自动化（BrowserSkill / local-browser）
	FeatCutBrowserSkill = true
	// FeatCutWebSearch 网页搜索与网页搜索服务商（爬虫/联网）
	// 放开：对话输入框提供「联网搜索」开关（默认关闭），开启后由
	//       web_search 阶段检索网页并把结果并入证据，因此需要搜索引擎管理
	//       接口（配置 Brave/Tavily/SearXNG 等）与检索接口本身。
	FeatCutWebSearch = false
	// FeatCutOrganization 组织、邀请成员、跨租户共享
	FeatCutOrganization = true
	// FeatCutAutomation 多步骤工作流 / 定时自动化
	FeatCutAutomation = true
	// FeatCutSandboxDesktop 沙箱「桌面」Tab（noVNC）；仅保留产物 + 交互终端
	FeatCutSandboxDesktop = true
	// FeatCutAuditReport 审计报表 / 审计日志查询（单租户简化模式）
	FeatCutAuditReport = true
	// FeatCutMemory 记忆模块
	FeatCutMemory = true
	// FeatCutPasswordAuth 账号密码认证面（注册、邀请注册、密码登录、改密、OIDC 单点登录）。
	//
	// 交付形态的进入凭证只有一张授权码：入口页校验通过后，桌面壳用一次性
	// 令牌调 /auth/auto-setup 静默建会话，用户从头到尾不接触账号密码。
	// 保留这些接口只会多出一条「绕过授权码」的路径（浏览器直连前端即可尝试
	// 密码登录，OIDC 还会把用户带去外部 IdP），因此整组不再注册，请求直接 404。
	//
	// 仍然保留的认证接口：/auth/auto-setup（桌面一次性令牌）、/auth/refresh、
	// /auth/logout、/auth/validate、/auth/me、/auth/me/preferences、
	// /auth/switch-tenant、/auth/config。
	//
	// 需要回到上游的账号密码登录（例如 cmd/server 本地调试多人权限），
	// 把此常量改成 false 重新编译即可（真值定义在 middleware.PasswordAuthDisabled，
	// 因为认证中间件也要用同一个开关）。
	FeatCutPasswordAuth = middleware.PasswordAuthDisabled
)

// RouterParams 路由参数
type RouterParams struct {
	dig.In

	Config *config.Config
	// LicenseService 是授权服务，nil 表示不启用授权闸门。
	// 用指针 + nil 判断而不是额外的布尔开关：容器里没注册就是 nil，
	// 不需要再维护一个「是否启用」的配置项与它保持同步。
	LicenseService               *licenser.Service
	FileService                  interfaces.FileService
	UserService                  interfaces.UserService
	KBService                    interfaces.KnowledgeBaseService
	KnowledgeService             interfaces.KnowledgeService
	ChunkService                 interfaces.ChunkService
	SessionService               interfaces.SessionService
	MessageService               interfaces.MessageService
	ModelService                 interfaces.ModelService
	EvaluationService            interfaces.EvaluationService
	KBShareService               interfaces.KBShareService
	AgentShareService            interfaces.AgentShareService
	KBHandler                    *handler.KnowledgeBaseHandler
	GraphSearchHandler           *handler.GraphSearchHandler
	KnowledgeHandler             *handler.KnowledgeHandler
	TenantHandler                *handler.TenantHandler
	TenantService                interfaces.TenantService
	TenantAPIKeyService          interfaces.TenantAPIKeyService
	TenantMemberService          interfaces.TenantMemberService
	TenantMemberHandler          *handler.TenantMemberHandler
	TenantInvitationHandler      *handler.TenantInvitationHandler
	AuditLogHandler              *handler.AuditLogHandler
	AuditLogService              interfaces.AuditLogService
	ChunkHandler                 *handler.ChunkHandler
	SessionHandler               *session.Handler
	ProjectHandler               *handler.ProjectHandler
	MessageHandler               *handler.MessageHandler
	MessageSuggestionHandler     *handler.MessageSuggestionHandler
	ModelHandler                 *handler.ModelHandler
	ModelCredentialsHandler      *handler.ModelCredentialsHandler
	SandboxConfigHandler         *handler.SandboxConfigHandler
	SandboxSkillHandler          *handler.SandboxSkillHandler
	MeEnvVarHandler              *handler.MeEnvVarHandler
	CredentialVaultHandler       *handler.CredentialVaultHandler
	EvaluationHandler            *handler.EvaluationHandler
	AuthHandler                  *handler.AuthHandler
	InitializationHandler        *handler.InitializationHandler
	SystemHandler                *handler.SystemHandler
	MCPServiceHandler            *handler.MCPServiceHandler
	MCPCredentialsHandler        *handler.MCPCredentialsHandler
	MCPOAuthHandler              *handler.MCPOAuthHandler
	WebSearchHandler             *handler.WebSearchHandler
	WebSearchProviderHandler     *handler.WebSearchProviderHandler
	WebSearchCredentialsHandler  *handler.WebSearchProviderCredentialsHandler
	VectorStoreHandler           *handler.VectorStoreHandler
	StorageBackendHandler        *handler.StorageBackendHandler
	StorageBackendResolver       interfaces.StorageBackendResolver
	ResourceCatalog              interfaces.ResourceCatalog
	FAQHandler                   *handler.FAQHandler
	TagHandler                   *handler.TagHandler
	CustomAgentHandler           *handler.CustomAgentHandler
	UserFavoriteHandler          *handler.UserResourceFavoriteHandler
	SkillHandler                 *handler.SkillHandler
	OrganizationHandler          *handler.OrganizationHandler
	IMHandler                    *handler.IMHandler
	EmbedChannelHandler          *handler.EmbedChannelHandler
	EmbedChannelService          interfaces.EmbedChannelService
	MCPEndpointHandler           *handler.MCPEndpointHandler
	MCPEndpointService           interfaces.MCPEndpointService
	MCPServer                    *mcpserver.Server
	RedisClient                  *redis.Client
	DataSourceHandler            *handler.DataSourceHandler
	DataSourceCredentialsHandler *handler.DataSourceCredentialsHandler
	WeKnoraCloudHandler          *handler.WeKnoraCloudHandler
	WikiPageHandler              *handler.WikiPageHandler
	MemoryHandler                *handler.MemoryHandler
	AutomationHandler            *handler.AutomationHandler
	HostSandbox                  service.HostSandboxManager
	OperationDispatcher          *operations.HandlerDispatcher
}

// NewRouter 创建新的路由
func NewRouter(params RouterParams) *gin.Engine {
	r := gin.New()
	r.ContextWithFallback = true
	// 清理 FormFile/MultipartForm 解析产生的 multipart 临时文件，避免容器 /tmp 持续增长。
	r.Use(middleware.MultipartFormCleanup())

	// Trusted proxies: gin defaults to trusting ALL proxies, which makes
	// c.ClientIP() honor a client-supplied X-Forwarded-For. Public, unauthed
	// embed endpoints rate-limit per (channel, ClientIP), so a spoofed XFF would
	// trivially bypass the limiter. Restrict to the fronting proxy network so
	// only the real client IP (appended by nginx) is returned. Configurable via
	// WEKNORA_TRUSTED_PROXIES (comma-separated CIDRs/IPs).
	if err := r.SetTrustedProxies(trustedProxies()); err != nil {
		logger.Errorf(context.Background(), "[Router] failed to set trusted proxies: %v", err)
	}

	// CORS 中间件应放在最前面。
	// 注意：通配符 AllowOrigins 下浏览器会拒绝一切带凭据（cookie）的跨域
	// 请求（CORS 规范禁止 "*" 与 credentials 组合），因此 AllowCredentials
	// 实际只对未来改为回显具体 Origin 时才生效；当前认证全部走显式的
	// Authorization / X-API-Key 头，不依赖 ambient 凭据。若引入 cookie
	// 认证，必须先把 AllowOrigins 换成受控清单。
	r.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Origin", "Content-Type", "Accept", "Authorization", "X-API-Key", "X-Request-ID", "X-Tenant-ID",
			"X-Embed-Session", "X-Embed-Visitor", "X-External-User-ID", "X-External-User-Token", "X-WeKnora-Desktop-Token",
			// 桌面端 API 直连本机 HTTP（Wails WebKit 自定义协议无法携带请求体），
			// 这些头会触发 CORS 预检，必须放行。
			"Accept-Language", "Range",
			// Streamable HTTP MCP clients running in a browser send these on
			// the /mcp/:endpoint_id surface.
			"MCP-Protocol-Version", "Mcp-Method", "Mcp-Name", "Mcp-Session-Id", "Last-Event-ID",
		},
		ExposeHeaders:    []string{"Content-Length", "Content-Range", "Accept-Ranges", "Access-Control-Allow-Origin", "Mcp-Session-Id"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 基础中间件（不需要认证）
	r.Use(middleware.RequestID())
	r.Use(middleware.Language())
	r.Use(middleware.Logger())
	r.Use(middleware.Recovery())
	r.Use(middleware.ErrorHandler())

	// 健康检查（不需要认证）
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Swagger API 文档（仅在非生产环境下启用）
	// 通过 GIN_MODE 环境变量判断：release 模式下禁用 Swagger
	if gin.Mode() != gin.ReleaseMode {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler,
			ginSwagger.DefaultModelsExpandDepth(-1), // 默认折叠 Models
			ginSwagger.DocExpansion("list"),         // 展开模式: "list"(展开标签), "full"(全部展开), "none"(全部折叠)
			ginSwagger.DeepLinking(true),            // 启用深度链接
			ginSwagger.PersistAuthorization(true),   // 持久化认证信息
		))
	}

	// Embed page framing policy: emit a per-channel `frame-ancestors` CSP so the
	// embed SPA page (/embed/:channelId) can only be iframed by the channel's
	// allowed origins. This is the page-level counterpart to the API Origin
	// allowlist enforced in EmbedAuth. Registered before the static handler so
	// it runs for the embed HTML response.
	if params.EmbedChannelService != nil {
		r.Use(embedFrameAncestorsMiddleware(params.EmbedChannelService))
	}

	// Lite bundles the frontend; other editions can explicitly opt in.
	if handler.Edition == "lite" || strings.TrimSpace(os.Getenv("WEKNORA_WEB_DIR")) != "" {
		serveFrontendStatic(r)
	}

	// IM 回调路由（在认证中间件之前注册，使用各平台自身的签名验证）
	// OEM: IM 外部渠道不在交付范围内，保留注册不拦截（不影响站内功能）；
	//      如需彻底关闭改 FeatCutIM。此处保持原样以免影响现有会话。
	RegisterIMRoutes(r, params.IMHandler)

	// Web embed 公开路由（使用 publish token 鉴权，不走全局 Auth）
	RegisterEmbedPublicRoutes(
		r,
		params.EmbedChannelHandler,
		params.EmbedChannelService,
		params.TenantService,
		params.RedisClient,
		params.FileService,
		params.StorageBackendResolver,
		params.ResourceCatalog,
	)

	// Workspace MCP server surface (/mcp/:endpoint_id): bearer-token auth per
	// endpoint, so it must precede the global Auth middleware.
	// OEM: MCP 已整体关闭（FeatCutMCP），不再暴露 MCP Server 入口。
	if !FeatCutMCP {
		RegisterMCPServerRoutes(r, params.MCPServer, params.MCPEndpointService, params.TenantService)
	}

	// Short-lived capability URLs for IM and other clients that cannot attach
	// WeKnora authentication headers.
	serveResourceGrants(r, params.ResourceCatalog, params.TenantService, params.FileService, params.StorageBackendResolver)

	// Sandbox terminal WebSocket (self-authenticated via a short-lived
	// query ticket — see RegisterSandboxTerminalRoutes; browsers cannot set
	// auth headers on the WS handshake, so this must precede the global Auth
	// middleware). The ticket is minted by an authenticated POST.
	// OEM: 沙箱面板只保留「产物」+「交互终端」，故终端 WS 保留、桌面 WS 关闭。
	RegisterSandboxTerminalRoutes(r, params.SessionHandler)
	if !FeatCutSandboxDesktop {
		RegisterSandboxDesktopRoutes(r, params.SessionHandler)
	}
	// OEM: 浏览器自动化（BrowserSkill / local-browser）整体关闭。
	if !FeatCutBrowserSkill {
		r.GET("/api/v1/local-browser/extension", params.SessionHandler.BrowserSkillExtension)
		r.POST("/api/v1/local-browser/extension/authorize", params.SessionHandler.BrowserSkillAuthorize)
		r.POST("/api/v1/local-browser/internal", params.SessionHandler.BrowserSkillInternal)
	}

	// 认证中间件
	r.Use(middleware.Auth(params.TenantService, params.UserService, params.TenantMemberService, params.TenantAPIKeyService, params.Config))

	// 授权闸门：放在认证之后、业务路由之前。
	// 顺序很重要——先认证才能让「无授权仍可登录」这条规则生效，
	// 否则用户在授权页之前就被 401 挡在门外。
	r.Use(middleware.License(params.LicenseService))

	// 文件服务：统一代理本地/MinIO/COS/TOS存储后端（需要认证）
	serveFilesWithResources(r, params.FileService, params.StorageBackendResolver, params.ResourceCatalog)

	// Presigned file access: no auth required, signature-verified.
	servePresignedFiles(r, params.TenantService, params.StorageBackendResolver)

	// Diagnostic preview of presigned URLs (Admin only, behind auth middleware).
	servePresignedPreview(r, params.Config, params.StorageBackendResolver, params.ResourceCatalog)

	// Langfuse observability — only active when LANGFUSE_* env vars are set.
	// The middleware is registered unconditionally; when disabled it's a no-op.
	r.Use(langfuse.GinMiddleware())

	// Audit log injection — middleware/rbac.go's reject paths and the
	// admin-only /tenants/:id/audit-log endpoint pull the service out
	// of the gin context. Provider is a no-op when AuditLogService is
	// nil (e.g. lite mode without DB), so the rbac path degrades to
	// "log to stderr only" instead of crashing.
	r.Use(middleware.AuditServiceProvider(params.AuditLogService))

	// 需要认证的API路由
	v1 := r.Group("/api/v1")
	{
		// rbacGuards bundles the role-gating middleware factories so each
		// Register* function below can attach the right guard without
		// taking a *config.Config dependency directly. The guards honour
		// cfg.Tenant.EnableRBAC: when false, they log but pass through,
		// preserving today's behaviour during the rollout window.
		rbacGuards := newRBACGuards(
			params.Config,
			params.KBHandler,
			params.CustomAgentHandler,
			params.KnowledgeHandler,
			params.ChunkHandler,
			params.WikiPageHandler,
			params.KBService,
			params.KnowledgeService,
			params.ChunkService,
			params.KBShareService,
			params.AgentShareService,
		)

		// API-key gate: single authority for X-API-Key principals. Runs
		// first on every /api/v1 route (JWT sessions pass straight
		// through) and denies any route not explicitly declared via the
		// apiKeyGroup helpers. Must be attached BEFORE the Register* calls
		// so that sub-groups inherit it.
		v1.Use(rbacGuards.apiKeyAuthorizer.Middleware())

		// OEM：账号密码认证面整组不注册时，/auth/config 也要如实回报
		//       「密码登录/OIDC 不可用」，否则运维按配置判断会误以为接口还在。
		handler.SetPasswordLoginDisabled(FeatCutPasswordAuth)
		RegisterAuthRoutes(v1, params.AuthHandler, rbacGuards)
		// 授权接口：GET 无授权也要能读（拿机器码），POST 是导入授权码本身。
		RegisterLicenseRoutes(v1, licenser.NewHandler(params.LicenseService))
		RegisterTenantRoutes(v1, params.TenantHandler, params.TenantMemberHandler, params.TenantInvitationHandler, params.AuditLogHandler, rbacGuards)
		RegisterMyInvitationRoutes(v1, params.TenantInvitationHandler)
		RegisterKnowledgeBaseRoutes(v1, params.KBHandler, rbacGuards)
		RegisterImageAttrRoutes(v1, params.KBHandler, rbacGuards)
		RegisterKnowledgeBaseActivityRoutes(v1, params.AuditLogHandler, rbacGuards)
		// KB-scoped image proxy: lets tenants render images embedded in
		// org-shared / agent-visible KB content, which the tenant-scoped
		// /files route cannot serve because it enforces same-tenant paths.
		serveKBScopedFiles(
			v1,
			rbacGuards,
			params.TenantService,
			params.FileService,
			params.StorageBackendResolver,
			params.ResourceCatalog,
		)
		// Message-scoped image proxy: shared-agent replies belong to the
		// caller's session but may reference resources stored in the agent's
		// source workspace. Authorization is derived from the persisted message,
		// never from a client-provided workspace ID. Replies produced by the
		// caller's own agent over an org-shared KB fall back to the KB share
		// relation instead (#3022).
		serveMessageScopedFiles(
			v1,
			rbacGuards,
			params.MessageService,
			params.AgentShareService,
			params.TenantService,
			params.FileService,
			params.StorageBackendResolver,
			params.ResourceCatalog,
			params.KBShareService,
			params.KBService,
			params.KnowledgeService,
		)
		RegisterKnowledgeTagRoutes(v1, params.TagHandler, rbacGuards)
		RegisterKnowledgeRoutes(v1, params.KnowledgeHandler, rbacGuards)
		// OEM: 知识图谱关闭
		if !FeatCutGraph {
			RegisterGraphSearchRoutes(v1, params.GraphSearchHandler, rbacGuards)
		}
		RegisterFAQRoutes(v1, params.FAQHandler, rbacGuards)
		RegisterChunkRoutes(v1, params.ChunkHandler, rbacGuards)
		RegisterSessionRoutes(v1, params.SessionHandler, params.MessageSuggestionHandler, rbacGuards)
		RegisterProjectRoutes(v1, params.ProjectHandler, rbacGuards)
		// 旧版 Office（.doc/.ppt/.xls）转 Markdown 供预览；转换器是包级绑定，
		// 没有需要注入的依赖，所以在这里直接构造。
		RegisterDocumentConvertRoutes(v1, handler.NewDocumentConvertHandler(), rbacGuards)
		RegisterChatRoutes(v1, params.SessionHandler, rbacGuards)
		RegisterMessageRoutes(v1, params.MessageHandler, rbacGuards)
		RegisterModelRoutes(v1, params.ModelHandler, params.ModelCredentialsHandler, rbacGuards)
		RegisterSandboxConfigRoutes(v1, params.SandboxConfigHandler, params.SandboxSkillHandler, rbacGuards)
		RegisterMyEnvVarRoutes(v1, params.MeEnvVarHandler)
		RegisterMyCredentialVaultRoutes(v1, params.CredentialVaultHandler)
		// OEM: 浏览器自动化账户入口关闭（与上面 local-browser 一致）
		if !FeatCutBrowserSkill {
			v1.GET("/me/browser", params.SessionHandler.BrowserSkillAccount)
			v1.GET("/me/browser/extension", params.SessionHandler.BrowserSkillDownload)
			v1.POST("/me/browser", params.SessionHandler.BrowserSkillAccount)
		}
		RegisterEvaluationRoutes(v1, params.EvaluationHandler, rbacGuards)
		RegisterInitializationRoutes(v1, params.InitializationHandler, rbacGuards)
		params.SystemHandler.BindDeploymentCapabilities(deploymentCapabilitiesFromRouter(params))
		RegisterSystemRoutes(v1, params.SystemHandler, rbacGuards)
		// OEM: 系统管理里的审计报表关闭（单租户简化）。
		//      注意：SystemAdminRoutes 还承载模型配置等管理能力，不能整体关闭，
		//      审计部分由下方 AuditLog 路由按需裁剪，此处保持注册。
		RegisterSystemAdminRoutes(v1, params.SystemHandler, params.AuditLogHandler, rbacGuards)
		// OEM: MCP 服务 / MCP 端点关闭
		if !FeatCutMCP {
			RegisterMCPServiceRoutes(v1, params.MCPServiceHandler, params.MCPCredentialsHandler, params.MCPOAuthHandler, rbacGuards)
			RegisterMCPEndpointRoutes(v1, params.MCPEndpointHandler, rbacGuards)
		}
		// OEM: 审批 / 提示解析（/agent/tool-approvals、/agent/prompts、/agent/grants）
		//      必须无条件注册：本机沙箱的「删除前确认」「拒绝」都走它，跟 MCP 无关。
		//      原先它们挂在 RegisterMCPServiceRoutes 里，FeatCutMCP 一关就整组消失，
		//      点「允许一次 / 拒绝」只会得到 404 page not found（v2.9.2 修复）。
		RegisterAgentApprovalRoutes(v1, params.MCPServiceHandler, params.MCPOAuthHandler, rbacGuards)
		// OEM: 网页搜索 / 网页搜索服务商（联网爬虫）关闭
		if !FeatCutWebSearch {
			RegisterWebSearchRoutes(v1, params.WebSearchHandler, rbacGuards)
			RegisterWebSearchProviderRoutes(v1, params.WebSearchProviderHandler, params.WebSearchCredentialsHandler, rbacGuards)
		}
		RegisterVectorStoreRoutes(v1, params.VectorStoreHandler, rbacGuards)
		RegisterStorageBackendRoutes(v1, params.StorageBackendHandler, rbacGuards)
		// OEM: 智能体（ReAct Agent / 编排）关闭 —— 这也同时使 AgentQA 端点不可达，
		//      从根上禁止插件被 Agent 自动链式调度。
		if !FeatCutAgents {
			RegisterCustomAgentRoutes(v1, params.CustomAgentHandler, rbacGuards)
		}
		RegisterUserFavoriteRoutes(v1, params.UserFavoriteHandler, rbacGuards)
		// OEM: 插件市场（skills）**完整保留**，含浏览 / 安装 / 卸载 / 配置。
		RegisterSkillRoutes(v1, params.SkillHandler, rbacGuards)
		// OEM: 组织、邀请成员、跨租户共享关闭（单租户）
		if !FeatCutOrganization {
			RegisterOrganizationRoutes(v1, params.OrganizationHandler, rbacGuards)
		}
		RegisterIMChannelRoutes(v1, params.IMHandler, rbacGuards)
		RegisterEmbedChannelRoutes(v1, params.EmbedChannelHandler, rbacGuards)
		// OEM: 第三方数据源同步关闭
		if !FeatCutDataSource {
			RegisterDataSourceRoutes(v1, params.DataSourceHandler, params.DataSourceCredentialsHandler, rbacGuards)
		}
		RegisterWeKnoraCloudRoutes(v1, params.WeKnoraCloudHandler, rbacGuards)
		// OEM: Wiki 完整保留（编辑 / 新建 / 版本 / 自动生成），Wiki 检索与文档同桶。
		RegisterWikiPageRoutes(v1, params.WikiPageHandler, rbacGuards)
		// OEM: 记忆模块关闭
		if !FeatCutMemory {
			RegisterMemoryRoutes(v1, params.MemoryHandler, rbacGuards)
		}
		// OEM: 多步骤工作流 / 自动化关闭
		if !FeatCutAutomation {
			RegisterAutomationRoutes(v1, params.AutomationHandler, rbacGuards)
		}
		RegisterChunkerDebugRoutes(v1, rbacGuards)

		// Fail fast if any declared API-key policy points at a route
		// template that does not actually exist (typo / path drift). A
		// stale template would silently 403 every API key on that route,
		// so we panic at startup instead of shipping a dead policy.
		rbacGuards.assertAPIKeyPoliciesMatchRoutes(r)
	}

	if params.OperationDispatcher != nil {
		assertOperationRoutesExist(r, operations.Builtin())
		params.OperationDispatcher.Bind(r)
	}

	return r
}

// trustedProxies returns the proxy CIDRs/IPs whose X-Forwarded-For headers
// gin should trust when resolving the client IP. Defaults to loopback and
// private ranges (covers the bundled nginx in a container network); override
// with WEKNORA_TRUSTED_PROXIES (comma-separated). An explicit empty value
// disables proxy trust entirely so ClientIP() returns the direct peer.
func trustedProxies() []string {
	raw, ok := os.LookupEnv("WEKNORA_TRUSTED_PROXIES")
	if !ok {
		return []string{
			"127.0.0.0/8",
			"::1/128",
			"10.0.0.0/8",
			"172.16.0.0/12",
			"192.168.0.0/16",
			"fc00::/7",
		}
	}
	proxies := make([]string, 0)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			proxies = append(proxies, p)
		}
	}
	return proxies
}
