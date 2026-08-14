package handler

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubModelService 只实现 upsert 路径用到的方法，其余经接口嵌入 panic 暴露。
type stubModelService struct {
	interfaces.ModelService

	models          []*types.Model
	createCalls     int
	updateCalls     int
	credCalls       int
	lastUpdated     *types.Model
	lastCredID      string
	lastCredAPIKey  string
	createIDCounter int
}

func (s *stubModelService) CreateModel(_ context.Context, model *types.Model) error {
	s.createCalls++
	s.createIDCounter++
	if model.ID == "" {
		model.ID = "model-new-" + string(rune('0'+s.createIDCounter))
	}
	model.Status = types.ModelStatusActive
	s.models = append(s.models, model)
	return nil
}

func (s *stubModelService) ListModels(context.Context) ([]*types.Model, error) {
	return s.models, nil
}

func (s *stubModelService) UpdateModel(_ context.Context, model *types.Model) error {
	s.updateCalls++
	s.lastUpdated = model
	return nil
}

func (s *stubModelService) UpdateModelCredentials(
	_ context.Context, id string, apiKey, _ *string,
) (*types.Model, error) {
	s.credCalls++
	s.lastCredID = id
	if apiKey != nil {
		s.lastCredAPIKey = *apiKey
		for _, m := range s.models {
			if m.ID == id {
				m.Parameters.APIKey = *apiKey
			}
		}
	}
	return nil, nil
}

func remoteChatModel(name, provider string) *types.Model {
	return &types.Model{
		Name:   name,
		Type:   types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL: "https://api.example.com/v1",
			APIKey:  "sk-1",
			Provider: provider,
		},
	}
}

func TestUpsertWorkspaceModel_CreateWhenMissing(t *testing.T) {
	svc := &stubModelService{}
	bound, created, err := upsertWorkspaceModel(context.Background(), svc, nil, remoteChatModel("gpt-x", "openai"))

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 1, svc.createCalls)
	assert.NotEmpty(t, bound.ID)
}

func TestUpsertWorkspaceModel_ReuseWhenIdentical(t *testing.T) {
	existing := remoteChatModel("gpt-x", "openai")
	existing.ID = "model-1"
	svc := &stubModelService{models: []*types.Model{existing}}

	bound, created, err := upsertWorkspaceModel(context.Background(), svc, svc.models, remoteChatModel("gpt-x", "openai"))

	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "model-1", bound.ID)
	assert.Zero(t, svc.createCalls)
	assert.Zero(t, svc.updateCalls)
	assert.Zero(t, svc.credCalls)
}

func TestUpsertWorkspaceModel_UpdateOnDrift(t *testing.T) {
	existing := remoteChatModel("gpt-x", "openai")
	existing.ID = "model-1"
	svc := &stubModelService{models: []*types.Model{existing}}

	desired := remoteChatModel("gpt-x", "openai")
	desired.Parameters.BaseURL = "https://api2.example.com/v1"
	desired.Parameters.APIKey = "sk-2"

	bound, created, err := upsertWorkspaceModel(context.Background(), svc, svc.models, desired)

	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "model-1", bound.ID)
	assert.Zero(t, svc.createCalls)
	assert.Equal(t, 1, svc.updateCalls)
	assert.Equal(t, "https://api2.example.com/v1", svc.lastUpdated.Parameters.BaseURL)
	assert.Equal(t, 1, svc.credCalls)
	assert.Equal(t, "model-1", svc.lastCredID)
	assert.Equal(t, "sk-2", svc.lastCredAPIKey)
}

func TestUpsertWorkspaceModel_EmbeddingDimensionDrift(t *testing.T) {
	existing := &types.Model{
		ID:     "emb-1",
		Name:   "bge-m3",
		Type:   types.ModelTypeEmbedding,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:             "https://api.example.com/v1",
			Provider:            "openai",
			EmbeddingParameters: types.EmbeddingParameters{Dimension: 1024},
		},
	}
	svc := &stubModelService{models: []*types.Model{existing}}

	desired := &types.Model{
		Name:   "bge-m3",
		Type:   types.ModelTypeEmbedding,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:             "https://api.example.com/v1",
			Provider:            "openai",
			EmbeddingParameters: types.EmbeddingParameters{Dimension: 2048},
		},
	}

	_, created, err := upsertWorkspaceModel(context.Background(), svc, svc.models, desired)

	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, 1, svc.updateCalls)
	assert.Equal(t, 2048, svc.lastUpdated.Parameters.EmbeddingParameters.Dimension)
}

func TestUpsertWorkspaceModel_BuiltinOnlyReused(t *testing.T) {
	existing := remoteChatModel("gpt-x", "openai")
	existing.ID = "builtin-1"
	existing.IsBuiltin = true
	svc := &stubModelService{models: []*types.Model{existing}}

	desired := remoteChatModel("gpt-x", "openai")
	desired.Parameters.BaseURL = "https://changed.example.com/v1"
	desired.Parameters.APIKey = "sk-9"

	bound, created, err := upsertWorkspaceModel(context.Background(), svc, svc.models, desired)

	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "builtin-1", bound.ID)
	assert.Zero(t, svc.updateCalls, "builtin 模型不应被更新")
	assert.Zero(t, svc.credCalls, "builtin 模型凭证不应被轮换")
}

func TestBindModelsToKnowledgeBase(t *testing.T) {
	kb := &types.KnowledgeBase{WikiConfig: &types.WikiConfig{}}
	models := []*types.Model{
		{ID: "emb-1", Type: types.ModelTypeEmbedding},
		{ID: "chat-1", Type: types.ModelTypeKnowledgeQA},
		{ID: "vlm-1", Type: types.ModelTypeVLLM},
	}

	bindModelsToKnowledgeBase(kb, models)

	assert.Equal(t, "emb-1", kb.EmbeddingModelID)
	assert.Equal(t, "chat-1", kb.SummaryModelID)
	require.NotNil(t, kb.WikiConfig)
	assert.Equal(t, "chat-1", kb.WikiConfig.SynthesisModelID, "synthesis 应与对话模型显式同值")
	assert.True(t, kb.VLMConfig.Enabled)
	assert.Equal(t, "vlm-1", kb.VLMConfig.ModelID)
}

func TestBindModelsToKnowledgeBase_NilWikiConfig(t *testing.T) {
	kb := &types.KnowledgeBase{}
	bindModelsToKnowledgeBase(kb, []*types.Model{{ID: "chat-1", Type: types.ModelTypeKnowledgeQA}})

	assert.Equal(t, "chat-1", kb.SummaryModelID)
	assert.Nil(t, kb.WikiConfig, "WikiConfig 为 nil 时不应创建")
}
