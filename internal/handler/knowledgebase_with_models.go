package handler

import (
	"context"
	"net/http"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// CreateKnowledgeBaseWithModelsRequest is the request body for the combined
// create-knowledge-base-with-models endpoint. It carries the knowledge base
// parameters plus the model configurations to bind to the new knowledge base.
//
// Model handling follows the "create if missing, reuse if present" policy:
// each model config is matched against the caller's workspace by the dedup
// key (tenant + type + provider + name); a match reuses the existing model,
// otherwise the model is created in the workspace first and then bound.
type CreateKnowledgeBaseWithModelsRequest struct {
	// KnowledgeBase carries the knowledge base parameters
	// (name, type, chunking_config, etc.).
	KnowledgeBase types.KnowledgeBase `json:"knowledge_base"`
	// Models carries the model configurations to create/reuse and bind.
	Models []CreateKnowledgeBaseModelConfig `json:"models"`
}

// CreateKnowledgeBaseModelConfig is one model configuration block. Fields
// mirror the model creation request (POST /api/v1/models) so callers can
// describe a model without creating it separately.
type CreateKnowledgeBaseModelConfig struct {
	Name           string            `json:"name"`
	DisplayName    string            `json:"display_name"`
	Type           types.ModelType   `json:"type"`
	Source         types.ModelSource `json:"source"`
	Provider       string            `json:"provider"`
	Description    string            `json:"description"`
	BaseURL        string            `json:"base_url"`
	APIKey         string            `json:"api_key"`
	InterfaceType  string            `json:"interface_type"`
	Dimension      int               `json:"dimension"`
	SupportsVision bool              `json:"supports_vision"`
}

// CreateKnowledgeBaseWithModels godoc
// @Summary      按模型配置创建知识库
// @Description  创建知识库，并按其携带的模型配置在当前工作空间中自动创建或复用模型后绑定。
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        request  body      CreateKnowledgeBaseWithModelsRequest  true  "知识库信息与模型配置"
// @Success      201      {object}  map[string]interface{}  "创建的知识库与绑定模型"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/with-models [post]
func (h *KnowledgeBaseHandler) CreateKnowledgeBaseWithModels(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating knowledge base with models")

	// Parse request body
	var req CreateKnowledgeBaseWithModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(apperrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if len(req.Models) == 0 {
		c.Error(apperrors.NewBadRequestError("models cannot be empty"))
		return
	}
	kb := req.KnowledgeBase
	if err := validateExtractConfig(kb.ExtractConfig); err != nil {
		logger.Error(ctx, "Invalid extract configuration", err)
		c.Error(err)
		return
	}
	types.NormalizeKnowledgeBasePromptInstructions(&kb)
	if err := validateKnowledgeBasePromptInstructions(&kb); err != nil {
		c.Error(err)
		return
	}
	provider := strings.ToLower(strings.TrimSpace(kb.GetStorageProvider()))
	if provider != "" && !isStorageProviderAllowed(provider) {
		c.Error(apperrors.NewBadRequestError("Storage provider is not allowed by STORAGE_ALLOW_LIST"))
		return
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(apperrors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	// List existing workspace models once and reuse them by dedup key
	// (tenant + type + provider + name) instead of creating duplicates.
	existingModels, err := h.modelService.ListModels(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}

	var createdModels []*types.Model
	var boundModels []*types.Model
	for i := range req.Models {
		cfg := req.Models[i]
		model := &types.Model{
			TenantID:    tenantID,
			Name:        secutils.SanitizeForLog(cfg.Name),
			DisplayName: secutils.SanitizeForLog(cfg.DisplayName),
			Type:        types.ModelType(secutils.SanitizeForLog(string(cfg.Type))),
			Source:      cfg.Source,
			Description: secutils.SanitizeForLog(cfg.Description),
			Parameters: types.ModelParameters{
				BaseURL:             cfg.BaseURL,
				APIKey:              cfg.APIKey,
				InterfaceType:       cfg.InterfaceType,
				Provider:            cfg.Provider,
				SupportsVision:      cfg.SupportsVision,
				EmbeddingParameters: types.EmbeddingParameters{
					Dimension: cfg.Dimension,
				},
			},
		}

		existing := findModelByDedupKey(existingModels, model)
		if existing != nil {
			boundModels = append(boundModels, existing)
			continue
		}

		// Local (Ollama) models download asynchronously; binding a
		// downloading model to a knowledge base would leave it unusable.
		// This interface therefore only creates remote models — callers
		// that need a local model must pre-create it via POST /models.
		if model.Source == types.ModelSourceLocal {
			rollbackCreatedModels(ctx, h.modelService, createdModels)
			c.Error(apperrors.NewBadRequestError(
				"local (Ollama) models are downloaded asynchronously and cannot be bound immediately; " +
					"please pre-create the local model via POST /api/v1/models or use a remote model"))
			return
		}

		if err := h.modelService.CreateModel(ctx, model); err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			rollbackCreatedModels(ctx, h.modelService, createdModels)
			c.Error(apperrors.NewInternalServerError(err.Error()))
			return
		}
		createdModels = append(createdModels, model)
		boundModels = append(boundModels, model)
	}

	// Bind the models to the knowledge base by type, mirroring
	// initialization.go's applyKnowledgeBaseInitialization.
	for _, m := range boundModels {
		switch m.Type {
		case types.ModelTypeEmbedding:
			kb.EmbeddingModelID = m.ID
		case types.ModelTypeKnowledgeQA:
			kb.SummaryModelID = m.ID
		case types.ModelTypeVLLM:
			kb.VLMConfig.Enabled = true
			kb.VLMConfig.ModelID = m.ID
		}
	}

	logger.Infof(ctx, "Creating knowledge base with models, name: %s", secutils.SanitizeForLog(kb.Name))
	createdKB, err := h.service.CreateKnowledgeBase(ctx, &kb)
	if err != nil {
		// Rolling back created models keeps the workspace consistent when
		// the knowledge base itself fails to persist.
		rollbackCreatedModels(ctx, h.modelService, createdModels)
		if appErr, ok := apperrors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge base created successfully with models, ID: %s, name: %s",
		secutils.SanitizeForLog(createdKB.ID), secutils.SanitizeForLog(createdKB.Name))
	callerTenantID := c.GetUint64(types.TenantIDContextKey.String())
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"knowledge_base": buildKBResponse(createdKB, h.resolveKBStoreView(ctx, createdKB, callerTenantID), nil),
			"models":         dto.NewModelResponses(ctx, boundModels),
		},
	})
}

// findModelByDedupKey finds an existing workspace model matching the dedup
// key (type + provider + name; tenant is already scoped by ListModels).
func findModelByDedupKey(models []*types.Model, target *types.Model) *types.Model {
	for _, m := range models {
		if m.Type == target.Type &&
			m.Name == target.Name &&
			m.Parameters.Provider == target.Parameters.Provider {
			return m
		}
	}
	return nil
}

// rollbackCreatedModels hard-deletes the models created by this request so a
// partial failure does not leave orphaned model rows behind.
func rollbackCreatedModels(ctx context.Context, modelService interfaces.ModelService, created []*types.Model) {
	for _, m := range created {
		if err := modelService.DeleteModel(ctx, m.ID); err != nil {
			logger.Warnf(ctx, "Failed to roll back created model %s: %v", m.ID, err)
		}
	}
}
