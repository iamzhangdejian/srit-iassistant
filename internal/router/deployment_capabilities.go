package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
)

func deploymentCapabilitiesFromRouter(params RouterParams) handler.DeploymentCapabilitiesData {
	// OEM 改造：能力清单必须与路由裁剪保持一致。
	// 前端侧边栏/设置页通过 isSupported(capability) 决定入口是否渲染，
	// 若这里仍按「handler 非 nil」上报，会出现「路由已砍、菜单仍在」的错位。
	// 因此每一项都额外与 router.go 的 FeatCut* 开关做与运算。
	return handler.BuildDeploymentCapabilities(handler.Edition, handler.DeploymentFeatureAvailability{
		Organizations: params.OrganizationHandler != nil && !FeatCutOrganization,
		Agents:        params.CustomAgentHandler != nil && !FeatCutAgents,
		IM:            params.IMHandler != nil,
		// Match RegisterEmbedChannelRoutes: management routes depend on handler only.
		Embed: params.EmbedChannelHandler != nil,
		// Match RegisterMCPEndpointRoutes / RegisterMCPServerRoutes.
		MCPServer: params.MCPEndpointHandler != nil && params.MCPServer != nil && params.MCPEndpointService != nil && !FeatCutMCP,
		API:       params.TenantHandler != nil && params.TenantAPIKeyService != nil,
		MCP: params.MCPServiceHandler != nil &&
			params.MCPCredentialsHandler != nil &&
			params.MCPOAuthHandler != nil && !FeatCutMCP,
		WebSearch: params.WebSearchHandler != nil &&
			params.WebSearchProviderHandler != nil &&
			params.WebSearchCredentialsHandler != nil && !FeatCutWebSearch,
		VectorStore:           params.VectorStoreHandler != nil,
		Storage:               params.StorageBackendHandler != nil,
		Sandbox:               params.SandboxConfigHandler != nil,
		SandboxDocker:         sandbox.DockerBackendEnabled(),
		SandboxHost:           params.HostSandbox.Manager != nil,
		SandboxRemote:         params.SandboxConfigHandler != nil && !params.HostSandbox.Desktop,
		SandboxUserAllocation: allocation.UserModeAvailable(),
		// /api/v1/me/browser 与 local-browser 相关路由一起被 FeatCutBrowserSkill 关掉，
		// 前端据此停掉浏览器连接状态的轮询（否则每 30s 一次 404 警告）。
		BrowserSkill: params.SessionHandler != nil && !FeatCutBrowserSkill,
		// 项目目录来自 cmd/desktop 注入的原生选择框，与 host 沙箱解耦：
		// Windows 没有沙箱后端（winhost 报 unsupported），但项目照常可用。
		ProjectDir: handler.HostProjectDirSupported(),
	})
}
