package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterAgentTaskRoutes mounts the agent build task monitor endpoints.
//
// The whole group sits behind SystemAdmin(): the page is an operational console
// for the wiki build hand-off to the external agent gateway, and it shows rows
// from every knowledge base, so it is not scoped to a single KB or tenant.
//
// Routes are registered plain (not via apiKeyRoute) on purpose — this is an
// interactive admin console, and no platform API-key capability is needed to
// cover it, mirroring the /system/admin/promote style actions above it.
func RegisterAgentTaskRoutes(r *gin.RouterGroup, h *handler.AgentTaskHandler, g *rbacGuards) {
	if h == nil {
		return
	}
	agentTasks := r.Group("/system/admin/agent-tasks", g.SystemAdmin())
	{
		agentTasks.GET("", h.ListAgentTasks)
		agentTasks.GET("/summary", h.AgentTaskSummary)
		agentTasks.POST("/:id/retry", h.RetryAgentTask)
		agentTasks.POST("/:id/cancel", h.CancelAgentTask)
	}
}
