package router

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/gin-gonic/gin"
)

// OEM 改造：裁剪域名（operation ID 前缀）与被 FeatCut 开关关闭的能力一一对应。
// 这些域的 operation 对应的路由在 router.go 里已被刻意跳过注册，
// 若不做豁免，启动自检会把「主动裁剪」误判为「路由漂移」而 panic。
//
// 注意：这里只豁免**自检**，被裁剪路由本身依然不存在（403/404 语义由路由层决定），
// 能力清单里也不会再下发给 Agent（agent 能力整域已被 FeatCutAgents 关闭）。
var oemCutOperationDomains = map[string]bool{
	"agent":       true, // FeatCutAgents
	"automation":  true, // FeatCutAutomation
	"data_source": true, // FeatCutDataSource
	"mcp_service": true, // FeatCutMCP
	// web_search_provider 已放开（FeatCutWebSearch = false）：对话的联网搜索
	// 开关需要搜索引擎管理能力，因此不再豁免，其 operation 照常校验路由存在。
}

// oemCutOperationIDs 是域内个别需要单独豁免的 operation（按 ID 精确匹配）。
// workspace.list_members 属成员管理，对应 FeatCutOrganization。
var oemCutOperationIDs = map[string]bool{
	"workspace.list_members":     true,
	"knowledge_base.list_shared": true, // 依赖组织共享，随 FeatCutOrganization 关闭
}

// assertOperationRoutesExist panics when an agent catalog operation names a
// route the engine does not serve, so route drift fails at startup instead of
// turning an operation into a permanent 404.
//
// OEM 改造：先剔除被 FeatCut 开关主动裁剪的 operation，再断言。
func assertOperationRoutesExist(r *gin.Engine, catalog *operations.Catalog) {
	missing := missingOperationRoutes(r, catalog)
	if len(missing) > 0 {
		panic(fmt.Sprintf("agent operations point at unregistered routes: %v", missing))
	}
}

func missingOperationRoutes(r *gin.Engine, catalog *operations.Catalog) []string {
	served := make(map[string]bool)
	for _, route := range r.Routes() {
		served[route.Method+" "+route.Path] = true
	}
	var missing []string
	for _, op := range catalog.All() {
		// OEM: 被裁剪的能力域 / 单条 operation 不参与自检
		if oemCutOperationIDs[op.ID] || oemCutOperationDomains[operationDomain(op.ID)] {
			continue
		}
		if key := op.Method + " " + op.Route; !served[key] {
			missing = append(missing, op.ID+" ("+key+")")
		}
	}
	return missing
}

// operationDomain 取 operation ID 的域前缀，如 "agent.list" -> "agent"。
func operationDomain(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '.' {
			return id[:i]
		}
	}
	return id
}
