package repository

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Agent notification for wiki build.
//
// When a document finishes parsing (promoted to parse_status=completed) in a
// knowledge base whose IndexingStrategy.WikiEnabled is true AND
// CustomWikiGeneration is true (i.e. WeKnora deliberately does NOT auto-build
// wiki pages; an external agent is expected to), WeKnora fires a best-effort
// HTTP POST to the configured OpenAI-Agents gateway so its skill
// (e.g. supply-management-policy-compiler) can build the wiki pages for that
// document. This is intentionally asynchronous and fire-and-forget: a slow or
// down gateway must never block or fail the document promote.

// agentGatewayTimeout bounds the outgoing notification request.
const agentGatewayTimeout = 10 * time.Second

// maybeNotifyAgentForWikiBuild checks whether the completed document belongs to
// a wiki-enabled + custom-wiki-generation knowledge base and, if so, schedules
// an asynchronous POST to the agent gateway (configured via
// WIKI_AGENT_CALLBACK_URL). Called from FinalizeSubtask when the promote to
// "completed" wins.
func maybeNotifyAgentForWikiBuild(ctx context.Context, db *gorm.DB, knowledgeID string) {
	callbackURL := os.Getenv("WIKI_AGENT_CALLBACK_URL")
	if callbackURL == "" {
		return
	}

	// Load knowledge row for kb_id + file_name/title.
	var k struct {
		KnowledgeBaseID string `gorm:"column:knowledge_base_id"`
		FileName        string `gorm:"column:file_name"`
		Title           string `gorm:"column:title"`
	}
	if err := db.WithContext(ctx).Model(&types.Knowledge{}).
		Select("knowledge_base_id, file_name, title").
		Where("id = ?", knowledgeID).Take(&k).Error; err != nil {
		logger.Warnf(ctx, "[agent-notify] load knowledge %s failed: %v", knowledgeID, err)
		return
	}
	if k.KnowledgeBaseID == "" {
		return
	}

	// Load KB to decide wiki-enabled + custom-wiki-generation.
	var kb types.KnowledgeBase
	if err := db.WithContext(ctx).Model(&types.KnowledgeBase{}).
		Where("id = ?", k.KnowledgeBaseID).Take(&kb).Error; err != nil {
		logger.Warnf(ctx, "[agent-notify] load kb %s failed: %v", k.KnowledgeBaseID, err)
		return
	}
	if !kb.IsWikiEnabled() || !kb.CustomWikiGeneration {
		// WeKnora auto-generates wiki (or wiki not enabled): nothing to notify.
		return
	}

	docName := k.FileName
	if docName == "" {
		docName = k.Title
	}

	// 加载知识库绑定的摘要模型配置（名字/base_url/api_key），随任务传给
	// agent-gateway：技能脚本据此用知识库自己的模型做 wiki 构建（2026-09-10 WEK-46）。
	// ModelParameters.Scan 已自动解密 api_key（DecryptStoredSecretLenient），此处即明文。
	modelName, modelBaseURL, modelAPIKey := "", "", ""
	if kb.SummaryModelID != "" {
		var mdl types.Model
		if err := db.WithContext(ctx).Model(&types.Model{}).
			Where("id = ?", kb.SummaryModelID).Take(&mdl).Error; err == nil {
			modelName = mdl.Name
			modelBaseURL = mdl.Parameters.BaseURL
			modelAPIKey = mdl.Parameters.APIKey
		} else {
			logger.Warnf(ctx, "[agent-notify] load summary model %s for kb %s failed: %v",
				kb.SummaryModelID, kb.ID, err)
		}
	}

	// 技能名：从知识库 wiki_config.skill 读取（data_supply 新建/编辑知识库时指定），
	// 空则默认 supply-management-policy-compiler。用于通知 agent-gateway 用哪个技能构建。
	agentSkillName := ""
	if kb.WikiConfig != nil {
		agentSkillName = kb.WikiConfig.Skill
	}
	if agentSkillName == "" {
		agentSkillName = "supply-management-policy-compiler"
	}

	// Persist the hand-off instead of firing a detached goroutine: this row is
	// the durable queue the agent build dispatcher consumes, and it is what makes
	// the document visible, retryable and cancellable on the agent task page.
	enqueueAgentBuildTask(ctx, db, callbackURL, kb.ID, knowledgeID, docName,
		modelName, modelBaseURL, modelAPIKey, agentSkillName, kb.TenantID)
}

// enqueueAgentBuildTask renders the hand-off payload and writes it to
// agent_build_tasks as a `queued` row.
//
// The task input instructs the agent to run the wiki build skill for the given
// document, passing the exact kb_id and document id so the agent does not need
// to discover them. When the knowledge base has a bound summary model
// (SummaryModelID), its name/base_url/api_key are forwarded in config so the
// skill scripts use the KB's own model instead of the gateway default.
//
// The payload is stored verbatim and POSTed later by the agent build dispatcher,
// which caps how many builds are in flight at the gateway at any moment.
func enqueueAgentBuildTask(
	ctx context.Context,
	db *gorm.DB,
	callbackURL, kbID, knowledgeID, docName string,
	modelName, modelBaseURL, modelAPIKey, agentSkillName string,
	tenantID uint64,
) {

	// 技能名：随通知上下文传入（空则默认 supply-management-policy-compiler）。
	// 实际技能名在 maybeNotifyAgentForWikiBuild 里从 kb.WikiConfig.Skill 解析后传入。
	instructions := "使用 " + agentSkillName + " 技能为指定文档构建 WeKnora wiki 知识。" +
		"目标知识库 kb_id=" + kbID + "，文档 knowledge_id=" + knowledgeID +
		"（文件名: " + docName + "）。按技能 SKILL.md 的标准流程完整执行：" +
		"找文件→血缘/版本家族解析→建目录→摘要→实体→关键词→索引，并通过 wiki_log_write MCP 工具按大步骤回报进度。"

	cfg := map[string]interface{}{
		// 结构化任务上下文：runner 注入 WEKNORA_KB_ID / WEKNORA_KNOWLEDGE_ID
		// 环境变量，技能脚本据此覆盖 config.yaml 的固定 kb_id（多库动态触发）。
		"kb_id":        kbID,
		"knowledge_id": knowledgeID,
		"doc_name":     docName,
		"skill":        agentSkillName,
	}
	// 知识库绑定模型的配置：runner 注入 WEKNORA_LLM_MODEL / WEKNORA_LLM_BASE_URL /
	// WEKNORA_LLM_API_KEY，技能脚本 llm_config() 优先读任务级配置。
	if modelName != "" {
		cfg["model"] = modelName
	}
	if modelBaseURL != "" {
		cfg["base_url"] = modelBaseURL
	}
	if modelAPIKey != "" {
		cfg["api_key"] = modelAPIKey
	}

	payload := map[string]interface{}{
		"input":        "为 WeKnora 文档构建 wiki 知识：kb_id=" + kbID + "，knowledge_id=" + knowledgeID + "，文件名=" + docName,
		"agent_name":   "",
		"instructions": instructions,
		"config":       cfg,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		logger.Warnf(ctx, "[agent-notify] marshal payload failed: %v", err)
		return
	}

	// One live build per document: re-parsing or re-uploading a file must not
	// stack duplicate builds on the gateway.
	var active int64
	if err := db.WithContext(ctx).Model(&types.AgentBuildTask{}).
		Where("knowledge_id = ?", knowledgeID).
		Where("status IN ?", []string{types.AgentBuildStatusQueued, types.AgentBuildStatusRunning}).
		Count(&active).Error; err != nil {
		logger.Warnf(ctx, "[agent-notify] duplicate check failed kb=%s knowledge=%s: %v", kbID, knowledgeID, err)
		return
	}
	if active > 0 {
		logger.Infof(ctx, "[agent-notify] wiki build already queued for knowledge=%s, skip duplicate", knowledgeID)
		return
	}

	now := time.Now()
	task := &types.AgentBuildTask{
		ID:              uuid.NewString(),
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
		DocName:         docName,
		Skill:           agentSkillName,
		Status:          types.AgentBuildStatusQueued,
		MaxAttempts:     types.DefaultAgentBuildMaxAttempts,
		GatewayURL:      callbackURL,
		Payload:         string(payloadJSON),
		QueuedAt:        now,
		NextAttemptAt:   now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := db.WithContext(ctx).Create(task).Error; err != nil {
		logger.Warnf(ctx, "[agent-notify] enqueue failed kb=%s knowledge=%s doc=%s: %v",
			kbID, knowledgeID, docName, err)
		return
	}
	logger.Infof(ctx, "[agent-notify] wiki build queued: id=%s kb=%s knowledge=%s doc=%s skill=%s",
		task.ID, kbID, knowledgeID, docName, agentSkillName)
}
