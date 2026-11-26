// Package router registers the HTTP API, including the human-in-the-loop
// approval endpoints the agent runtime pauses on.
package router

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 回归：审批 / 提示解析原先挂在 RegisterMCPServiceRoutes 里，FeatCutMCP=true 时
// 整组消失，本机沙箱的「允许一次 / 拒绝」点下去只会拿到 Gin 的
// `404 page not found`。现在它们由 RegisterAgentApprovalRoutes 无条件注册。
func TestAgentApprovalRoutesRegisterWithoutMCP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{}}}
	engine := gin.New()

	var routes []string
	require.NotPanics(t, func() {
		v1 := engine.Group("/api/v1")
		// mcpHandler 传零值：这里只验证路由注册，不调用 handler。
		RegisterAgentApprovalRoutes(v1, &handler.MCPServiceHandler{}, nil, g)
		for _, route := range engine.Routes() {
			routes = append(routes, route.Method+" "+route.Path)
		}
	})

	found := map[string]bool{}
	for _, r := range routes {
		found[r] = true
	}
	require.True(t, found["POST /api/v1/agent/tool-approvals/:pending_id"])
	require.True(t, found["POST /api/v1/agent/prompts/:prompt_id"])
	require.True(t, found["POST /api/v1/agent/secure-forms/:prompt_id"])
	require.True(t, found["GET /api/v1/agent/grants"])
	require.True(t, found["DELETE /api/v1/agent/grants/:id"])
	// oauthHandler 为 nil（MCP 未装配）时不应该注册 MCP OAuth 解析路由。
	require.False(t, found["POST /api/v1/agent/mcp-oauth-resolutions/:pending_id"])
}

// nil 的 MCPServiceHandler 不能让注册阶段 panic（server 形态参数可能为空）。
func TestAgentApprovalRoutesToleratesNilHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{}}}
	engine := gin.New()
	require.NotPanics(t, func() {
		RegisterAgentApprovalRoutes(engine.Group("/api/v1"), nil, nil, g)
	})
	require.Empty(t, engine.Routes())
}
