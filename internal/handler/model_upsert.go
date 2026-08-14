package handler

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// 本文件承载「工作空间模型 upsert」的公共能力，三处复用：
//   1. POST /knowledge-bases/with-models（建库时自动创建/修复模型并绑定）
//   2. POST /api/v1/models/upsert（编辑知识库模型前的独立 upsert 入口）
//   3. POST /initialization/user/init（建默认个人知识库时携带模型）
//
// upsert 语义：按去重键 (type + provider + name) 在调用方工作空间内匹配；
// 不存在则创建；存在但参数漂移（base_url / interface_type / dimension /
// supports_vision）则更新；api_key 非空且不同则经 credentials 子资源轮换。
// builtin 模型跨租户共享、仅 SystemAdmin 可改，命中时只复用不更新。

// modelFromConfig 把 with-models / user/init 的模型配置块转成 types.Model。
func modelFromConfig(cfg CreateKnowledgeBaseModelConfig, tenantID uint64) *types.Model {
	return &types.Model{
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
}

// upsertWorkspaceModel 在工作空间内 upsert 模型。existingModels 为调用方已
// 列出的工作空间模型（避免循环内重复 ListModels）；返回绑定用模型与是否新建。
func upsertWorkspaceModel(
	ctx context.Context,
	modelService interfaces.ModelService,
	existingModels []*types.Model,
	model *types.Model,
) (bound *types.Model, created bool, err error) {
	existing := findModelByDedupKey(existingModels, model)
	if existing == nil {
		if err := modelService.CreateModel(ctx, model); err != nil {
			return nil, false, err
		}
		logger.Infof(ctx, "Model created: type=%s name=%s id=%s", model.Type, model.Name, model.ID)
		return model, true, nil
	}

	// builtin 模型仅 SystemAdmin 可改，此处只复用
	if existing.IsBuiltin {
		return existing, false, nil
	}

	desired := model.Parameters
	current := &existing.Parameters
	if current.BaseURL != desired.BaseURL ||
		current.InterfaceType != desired.InterfaceType ||
		current.EmbeddingParameters.Dimension != desired.EmbeddingParameters.Dimension ||
		current.SupportsVision != desired.SupportsVision {
		current.BaseURL = desired.BaseURL
		current.InterfaceType = desired.InterfaceType
		current.EmbeddingParameters.Dimension = desired.EmbeddingParameters.Dimension
		current.SupportsVision = desired.SupportsVision
		if err := modelService.UpdateModel(ctx, existing); err != nil {
			return nil, false, err
		}
		logger.Infof(ctx, "Model parameters updated: id=%s", existing.ID)
	}
	if desired.APIKey != "" && desired.APIKey != current.APIKey {
		if _, err := modelService.UpdateModelCredentials(ctx, existing.ID, &desired.APIKey, nil); err != nil {
			return nil, false, err
		}
		logger.Infof(ctx, "Model credentials rotated: id=%s", existing.ID)
	}
	return existing, false, nil
}

// bindModelsToKnowledgeBase 按模型类型把模型绑定到知识库，与
// initialization.go 的 applyKnowledgeBaseInitialization 同源：
// embedding → EmbeddingModelID；KnowledgeQA → SummaryModelID 并同值写入
// WikiConfig.SynthesisModelID（显式同值，wiki 合成永远跟随对话模型）；
// VLLM → VLMConfig。
func bindModelsToKnowledgeBase(kb *types.KnowledgeBase, models []*types.Model) {
	for _, m := range models {
		switch m.Type {
		case types.ModelTypeEmbedding:
			kb.EmbeddingModelID = m.ID
		case types.ModelTypeKnowledgeQA:
			kb.SummaryModelID = m.ID
			if kb.WikiConfig != nil {
				kb.WikiConfig.SynthesisModelID = m.ID
			}
		case types.ModelTypeVLLM:
			kb.VLMConfig.Enabled = true
			kb.VLMConfig.ModelID = m.ID
		}
	}
}
