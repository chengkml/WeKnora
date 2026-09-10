package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
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
// WEKNORA_AGENT_CALLBACK_URL). Called from FinalizeSubtask when the promote to
// "completed" wins.
func maybeNotifyAgentForWikiBuild(ctx context.Context, db *gorm.DB, knowledgeID string) {
	callbackURL := os.Getenv("WEKNORA_AGENT_CALLBACK_URL")
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

	// Fire async (goroutine survives this call; detached from request ctx).
	go postAgentTask(callbackURL, kb.ID, knowledgeID, docName, modelName, modelBaseURL, modelAPIKey)
}

// postAgentTask POSTs a task to the OpenAI-Agents gateway. The task input
// instructs the agent to run the wiki build skill for the given document,
// passing the exact kb_id and document id so the agent does not need to
// discover them. When the knowledge base has a bound summary model
// (SummaryModelID), its name/base_url/api_key are forwarded in config so the
// skill scripts use the KB's own model instead of the gateway default.
func postAgentTask(callbackURL, kbID, knowledgeID, docName, modelName, modelBaseURL, modelAPIKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), agentGatewayTimeout)
	defer cancel()

	instructions := "使用 supply-management-policy-compiler 技能为指定文档构建 WeKnora wiki 知识。" +
		"目标知识库 kb_id=" + kbID + "，文档 knowledge_id=" + knowledgeID +
		"（文件名: " + docName + "）。按技能 SKILL.md 的标准流程完整执行：" +
		"找文件→血缘/版本家族解析→建目录→摘要→实体→关键词→索引，并通过 wiki_log_write MCP 工具按大步骤回报进度。"

	cfg := map[string]interface{}{
		// 结构化任务上下文：runner 注入 WEKNORA_KB_ID / WEKNORA_KNOWLEDGE_ID
		// 环境变量，技能脚本据此覆盖 config.yaml 的固定 kb_id（多库动态触发）。
		"kb_id":        kbID,
		"knowledge_id": knowledgeID,
		"doc_name":     docName,
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
	body, err := json.Marshal(payload)
	if err != nil {
		logger.Warnf(ctx, "[agent-notify] marshal payload failed: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		logger.Warnf(ctx, "[agent-notify] build request failed: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: agentGatewayTimeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Warnf(ctx, "[agent-notify] POST %s failed: %v", callbackURL, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		logger.Warnf(ctx, "[agent-notify] POST %s returned status %d", callbackURL, resp.StatusCode)
		return
	}
	logger.Infof(ctx, "[agent-notify] wiki build task submitted: kb=%s knowledge=%s doc=%s",
		kbID, knowledgeID, docName)
}
