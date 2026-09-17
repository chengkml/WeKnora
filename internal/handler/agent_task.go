package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// AgentTaskHandler serves the agent build task monitor: which documents are
// being built for wiki knowledge by the external OpenAI-Agents gateway, how many
// are waiting behind the concurrency cap, and the retry / cancel actions.
//
// Rows come from the agent_build_tasks ledger (migration 000078), not from the
// gateway, so the page keeps working while the gateway is unreachable and can
// show a document's whole build history instead of only the live view.
type AgentTaskHandler struct {
	service interfaces.AgentBuildTaskService
}

// NewAgentTaskHandler creates the agent build task handler.
func NewAgentTaskHandler(service interfaces.AgentBuildTaskService) *AgentTaskHandler {
	return &AgentTaskHandler{service: service}
}

// ListAgentTasks lists agent build tasks, newest first.
// @Summary      列出 agent 构建任务
// @Description  支持按状态 / 知识库 / 文档 / 文档名关键字过滤
// @Tags         system
// @Produce      json
// @Param        status query string false "queued/running/succeeded/failed/cancelled，可逗号分隔"
// @Param        knowledge_base_id query string false "知识库 ID"
// @Param        knowledge_id query string false "文档 ID"
// @Param        keyword query string false "文档名关键字"
// @Param        page query int false "页码，默认 1"
// @Param        page_size query int false "每页条数，默认 20，最大 100"
// @Success      200 {object} types.AgentBuildTaskPage
// @Router       /system/admin/agent-tasks [get]
func (h *AgentTaskHandler) ListAgentTasks(c *gin.Context) {
	page, pageSize, ok := parseListPagination(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid pagination parameters"})
		return
	}
	filter := types.AgentBuildTaskFilter{
		Status:          strings.TrimSpace(c.Query("status")),
		KnowledgeBaseID: strings.TrimSpace(c.Query("knowledge_base_id")),
		KnowledgeID:     strings.TrimSpace(c.Query("knowledge_id")),
		Keyword:         strings.TrimSpace(c.Query("keyword")),
		Page:            page,
		PageSize:        pageSize,
	}
	result, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		logger.Errorf(c.Request.Context(), "[agent-task] list failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list agent build tasks"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// AgentTaskSummary returns the counters shown on top of the monitor page.
// @Summary      agent 构建任务汇总
// @Tags         system
// @Produce      json
// @Success      200 {object} types.AgentBuildTaskSummary
// @Router       /system/admin/agent-tasks/summary [get]
func (h *AgentTaskHandler) AgentTaskSummary(c *gin.Context) {
	result, err := h.service.Summary(c.Request.Context())
	if err != nil {
		logger.Errorf(c.Request.Context(), "[agent-task] summary failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to summarize agent build tasks"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// RetryAgentTask re-queues a finished (failed / cancelled / succeeded) task.
// @Summary      重试 agent 构建任务
// @Tags         system
// @Produce      json
// @Param        id path string true "任务 ID"
// @Success      200 {object} object
// @Router       /system/admin/agent-tasks/{id}/retry [post]
func (h *AgentTaskHandler) RetryAgentTask(c *gin.Context) {
	h.act(c, true)
}

// CancelAgentTask cancels a queued or running task (also cancelled upstream).
// @Summary      取消 agent 构建任务
// @Tags         system
// @Produce      json
// @Param        id path string true "任务 ID"
// @Success      200 {object} object
// @Router       /system/admin/agent-tasks/{id}/cancel [post]
func (h *AgentTaskHandler) CancelAgentTask(c *gin.Context) {
	h.act(c, false)
}

// act runs the retry / cancel action and maps domain errors onto HTTP codes.
//
// A conflict (not 400) is deliberate: "the task already finished" and "the task
// is still pending" are races against the dispatcher, so the UI should re-read
// the row rather than treat the request as malformed.
func (h *AgentTaskHandler) act(c *gin.Context, retry bool) {
	ctx := c.Request.Context()
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing task id"})
		return
	}

	var err error
	action := "cancel"
	if retry {
		action = "retry"
		err = h.service.Retry(ctx, id)
	} else {
		err = h.service.Cancel(ctx, id)
	}

	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"success": true})
	case errors.Is(err, types.ErrAgentBuildTaskNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, types.ErrAgentBuildTaskNotRetryable),
		errors.Is(err, types.ErrAgentBuildTaskNotCancellable):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		logger.Errorf(ctx, "[agent-task] %s failed id=%s: %v", action, id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to " + action + " agent build task"})
	}
}
