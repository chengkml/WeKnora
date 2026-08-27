package handler

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

const maxTokenizeInputRunes = 10 * 1024

type TokenizeRequest struct {
	Text       string   `json:"text"`
	Mode       string   `json:"mode"`
	Stopwords  []string `json:"stopwords"`
}

type TokenizeResponse struct {
	Words []string `json:"words"`
	Count int      `json:"count"`
}

type TokenizerHandler struct{}

func NewTokenizerHandler() *TokenizerHandler {
	return &TokenizerHandler{}
}

// TokenizerHandler handles tokenize requests.
// Tokenize godoc
// @Summary      分词接口
// @Description  基于 Jieba 对输入文本进行分词，支持 cut / cut_for_search 模式及可选停用词过滤
// @Tags         分词
// @Accept       json
// @Produce      json
// @Param        request  body      TokenizeRequest   true  "{text, mode, stopwords}"
// @Success      200      {object}  map[string]interface{}  "分词结果"
// @Failure      400      {object}  map[string]interface{}  "请求参数错误"
// @Failure      413      {object}  map[string]interface{}  "文本超过长度限制"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /api/v1/tokenize [post]
func (h *TokenizerHandler) Post(c *gin.Context) {
	var req TokenizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "invalid request body: " + err.Error(),
		})
		return
	}

	text := strings.TrimSpace(req.Text)
	if text == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "text is required",
		})
		return
	}

	if utf8.RuneCountInString(text) > maxTokenizeInputRunes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"success": false,
			"error":   "text exceeds maximum length",
			"limit":   maxTokenizeInputRunes,
		})
		return
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "cut"
	}

	var words []string
	switch mode {
	case "cut":
		words = types.Jieba.Cut(text, true)
	case "cut_for_search":
		words = types.Jieba.CutForSearch(text, true)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "mode must be 'cut' or 'cut_for_search'",
		})
		return
	}

	if len(req.Stopwords) > 0 {
		stopset := make(map[string]struct{}, len(req.Stopwords))
		for _, s := range req.Stopwords {
			stopset[s] = struct{}{}
		}
		filtered := make([]string, 0, len(words))
		for _, w := range words {
			if _, ok := stopset[w]; !ok {
				filtered = append(filtered, w)
			}
		}
		words = filtered
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": TokenizeResponse{
			Words: words,
			Count: len(words),
		},
	})
}
