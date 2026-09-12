package handler

import (
	"io"
	"net/http"
	"os"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// SkillHandler handles skill-related HTTP requests
type SkillHandler struct {
	skillService interfaces.SkillService
}

// NewSkillHandler creates a new skill handler
func NewSkillHandler(skillService interfaces.SkillService) *SkillHandler {
	return &SkillHandler{
		skillService: skillService,
	}
}

// SkillInfoResponse represents the skill info returned to frontend
type SkillInfoResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListSkills godoc
// @Summary      获取预装Skills列表
// @Description  获取所有预装的Agent Skills元数据
// @Tags         Skills
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "Skills列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /skills [get]
func (h *SkillHandler) ListSkills(c *gin.Context) {
	ctx := c.Request.Context()

	skillsMetadata, err := h.skillService.ListPreloadedSkills(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to list skills: " + err.Error()))
		return
	}

	// Convert to response format
	var response []SkillInfoResponse
	for _, meta := range skillsMetadata {
		response = append(response, SkillInfoResponse{
			Name:        meta.Name,
			Description: meta.Description,
		})
	}

	// skills_available: true only when sandbox is enabled (docker or local), so frontend can hide/disable Skills UI
	sandboxMode := os.Getenv("WEKNORA_SANDBOX_MODE")
	skillsAvailable := sandboxMode != "" && sandboxMode != "disabled"

	logger.Infof(ctx, "skills_available: %v, sandboxMode: %s", skillsAvailable, sandboxMode)

	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"data":             response,
		"skills_available": skillsAvailable,
	})
}

// UploadSkill godoc
// @Summary      上传并安装技能
// @Description  上传技能 ZIP（含 SKILL.md），解压安装到 WeKnora 技能目录，并同步安装到 agent-gateway。Admin 权限。
// @Tags         Skills
// @Accept       multipart/form-data
// @Produce      json
// @Param        file         formData file true  "技能 ZIP 包（<skillname>/SKILL.md）"
// @Param        name         formData string false "可选技能名覆盖"
// @Success      200  {object}  map[string]interface{} "技能元数据"
// @Failure      400  {object}  errors.AppError "ZIP 或技能名非法"
// @Router       /skills/upload [post]
func (h *SkillHandler) UploadSkill(c *gin.Context) {
	ctx := c.Request.Context()

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.Error(errors.NewBadRequestError("请上传技能 ZIP 文件（字段名 file）"))
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewBadRequestError("读取上传文件失败"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewBadRequestError("读取上传文件失败"))
		return
	}

	forceName := c.PostForm("name")

	meta, err := h.skillService.UploadSkill(ctx, data, forceName)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"name":        meta.Name,
			"description": meta.Description,
		},
	})
}
