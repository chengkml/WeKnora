package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterAgentTaskRoutes mounts the agent build task monitor endpoints.
//
// Reads are open to every member of the active workspace instead of being
// SystemAdmin-only: this deployment has no system administrator account at all,
// and a SystemAdmin gate made the page unreachable (the SPA guard bounces
// non-admins to /platform/knowledge-bases, which is what operators saw as
// "clicking Agent 任务监控 opens the knowledge base page").
//
// The rows are safe to hand to a member because the handler scopes them to the
// caller's tenant (filter.TenantID = callerTenantScope(c)), so a member only
// sees builds belonging to their own workspace; a system administrator still
// sees every tenant. Mutations stay Admin+: retry / cancel act on the shared
// gateway queue, not on a single document.
//
// Routes are registered plain (not via apiKeyRoute) on purpose — this is an
// interactive console page and needs no platform API-key capability.
func RegisterAgentTaskRoutes(r *gin.RouterGroup, h *handler.AgentTaskHandler, g *rbacGuards) {
	if h == nil {
		return
	}
	agentTasks := r.Group("/agent-tasks")
	{
		agentTasks.GET("", g.Viewer(), h.ListAgentTasks)
		agentTasks.GET("/summary", g.Viewer(), h.AgentTaskSummary)
		// Execution log drawer: the gateway task snapshot plus its trace spans,
		// fetched server side so the browser needs no gateway credentials.
		agentTasks.GET("/:id/detail", g.Viewer(), h.GetAgentTaskDetail)
		agentTasks.POST("/:id/retry", g.Viewer(), h.RetryAgentTask)
		agentTasks.POST("/:id/cancel", g.Viewer(), h.CancelAgentTask)
	}
}
