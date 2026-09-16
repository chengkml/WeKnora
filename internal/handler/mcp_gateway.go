package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// MCPGatewayHandler handles MCP → agent-gateway synchronization requests.
//
// These endpoints do NOT touch the /mcp-services CRUD: WeKnora stays the source
// of truth for MCP config, and this handler only pushes a derived copy to the
// agent-gateway (the process that actually executes agent runs).
type MCPGatewayHandler struct {
	mcpGatewaySyncService *service.MCPGatewaySyncService
}

// NewMCPGatewayHandler creates a new MCP gateway sync handler
func NewMCPGatewayHandler(mcpGatewaySyncService *service.MCPGatewaySyncService) *MCPGatewayHandler {
	return &MCPGatewayHandler{
		mcpGatewaySyncService: mcpGatewaySyncService,
	}
}

// SyncMCPGateway godoc
// @Summary      同步MCP服务到Agent网关
// @Description  把当前空间的MCP服务配置全量覆盖下发到agent-gateway（幂等）
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "同步结果（synced/skipped/warnings）"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-gateway/sync [post]
func (h *MCPGatewayHandler) SyncMCPGateway(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	result, err := h.mcpGatewaySyncService.Sync(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"tenant_id": tenantID})
		c.Error(errors.NewInternalServerError("Failed to sync MCP services to agent-gateway: " + err.Error()))
		return
	}

	logger.Infof(ctx, "MCP services synced to agent-gateway: %d synced, %d skipped, %d warnings",
		len(result.Synced), len(result.Skipped), len(result.Warnings))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// MCPGatewayStatus godoc
// @Summary      查询Agent网关的MCP配置状态
// @Description  对比WeKnora与agent-gateway的MCP配置差异（drift检测）
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "网关状态"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-gateway/status [get]
func (h *MCPGatewayHandler) MCPGatewayStatus(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	status, err := h.mcpGatewaySyncService.Status(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"tenant_id": tenantID})
		c.Error(errors.NewInternalServerError("Failed to get MCP gateway status: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    status,
	})
}

// TestMCPGatewayServer godoc
// @Summary      通过Agent网关测试MCP服务
// @Description  以agent-gateway的视角测试MCP服务连通性并回显工具列表
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "MCP服务ID"
// @Success      200  {object}  map[string]interface{}  "网关侧测试结果"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /mcp-gateway/servers/{id}/test [post]
func (h *MCPGatewayHandler) TestMCPGatewayServer(c *gin.Context) {
	ctx := c.Request.Context()
	serviceID := secutils.SanitizeForLog(c.Param("id"))

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Testing MCP service through agent-gateway: %s", secutils.SanitizeForLog(serviceID))

	result, err := h.mcpGatewaySyncService.TestServer(ctx, tenantID, serviceID)
	if err != nil {
		// Mirrors TestMCPService: a failed probe is reported as data with
		// ok=false rather than an error envelope, so the UI can render the
		// reason inline. The gateway itself also answers 200 + ok=false for
		// unreachable servers, so the two shapes stay identical.
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": map[string]interface{}{
				"ok":    false,
				"error": err.Error(),
			},
		})
		return
	}

	logger.Infof(ctx, "MCP service gateway test completed: %s", secutils.SanitizeForLog(serviceID))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
