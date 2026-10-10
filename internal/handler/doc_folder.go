package handler

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// DocFolderHandler serves the multi-level document directory tree (doc_folders).
// One document belongs to exactly one folder; a parent folder's view
// recursively includes its whole subtree; non-empty folders cannot be deleted.
type DocFolderHandler struct {
	docFolderService interfaces.DocFolderService
}

// NewDocFolderHandler creates a doc folder handler.
func NewDocFolderHandler(docFolderService interfaces.DocFolderService) *DocFolderHandler {
	return &DocFolderHandler{docFolderService: docFolderService}
}

// validateDocFolderKB resolves the :id path param (KB id) and the effective
// tenant. Access control (and the tenant rewrite for shared KBs) is already
// enforced by the route-level KB-access guard, matching the tag handlers —
// this only pulls the two values out of the request.
func (h *DocFolderHandler) validateDocFolderKB(c *gin.Context) (string, uint64, error) {
	kbID := secutils.SanitizeForLog(c.Param("id"))
	if kbID == "" {
		return "", 0, errors.NewBadRequestError("Knowledge base ID cannot be empty")
	}
	tenantID := types.MustTenantIDFromContext(c.Request.Context())
	return kbID, tenantID, nil
}

// ListFolders godoc
// @Summary      获取文档目录树
// @Description  获取知识库的多级文档目录树（parent_id 空 = 根层级），每个目录带递归文档数和是否有子目录标记。
// @Tags         知识管理
// @Produce      json
// @Param        id        path   string  false "知识库ID"
// @Param        parent_id query  string  false "父目录ID（空 = 根目录）"
// @Success      200  {object}  types.DocFolderListResponse
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/doc-folders [get]
func (h *DocFolderHandler) ListFolders(c *gin.Context) {
	kbID, _, err := h.validateDocFolderKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	parentID := strings.TrimSpace(c.Query("parent_id"))
	folders, err := h.docFolderService.ListFolders(c.Request.Context(), kbID, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if folders == nil {
		folders = []types.DocFolderNode{}
	}
	c.JSON(http.StatusOK, types.DocFolderListResponse{ParentID: parentID, Folders: folders})
}

// CreateFolder godoc
// @Summary      创建文档目录
// @Description  在 parent_id 下创建新（空）文档目录
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id     path  string                       true  "知识库ID"
// @Param        folder body  types.DocFolderCreateRequest true  "目录数据"
// @Success      201  {object}  types.DocFolder
// @Failure      400  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/doc-folders [post]
func (h *DocFolderHandler) CreateFolder(c *gin.Context) {
	kbID, tenantID, err := h.validateDocFolderKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var req types.DocFolderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	folder, err := h.docFolderService.CreateFolder(c.Request.Context(), kbID, tenantID, strings.TrimSpace(req.ParentID), req.Name)
	if err != nil {
		writeDocFolderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, folder)
}

// UpdateFolder godoc
// @Summary      重命名或移动文档目录
// @Description  重命名和/或重新挂载一个目录；整个子树物化路径被重算
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id        path  string                        true  "知识库ID"
// @Param        folder_id path  string                        true  "目录ID"
// @Param        folder    body  types.DocFolderUpdateRequest true  "目录更新"
// @Success      200  {object}  types.DocFolder
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/doc-folders/{folder_id} [put]
func (h *DocFolderHandler) UpdateFolder(c *gin.Context) {
	kbID, _, err := h.validateDocFolderKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Folder ID is required"})
		return
	}
	var req types.DocFolderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	folder, err := h.docFolderService.RenameOrMoveFolder(
		c.Request.Context(), kbID, folderID, req.Name, strings.TrimSpace(req.ParentID), req.MoveParent)
	if err != nil {
		writeDocFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, folder)
}

// DeleteFolder godoc
// @Summary      删除空文档目录
// @Description  删除一个没有文档、没有子目录的目录
// @Tags         知识管理
// @Param        id        path string true "知识库ID"
// @Param        folder_id path string true "目录ID"
// @Success      204
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/doc-folders/{folder_id} [delete]
func (h *DocFolderHandler) DeleteFolder(c *gin.Context) {
	kbID, _, err := h.validateDocFolderKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Folder ID is required"})
		return
	}
	if err := h.docFolderService.DeleteFolder(c.Request.Context(), kbID, folderID); err != nil {
		writeDocFolderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// MoveDocuments godoc
// @Summary      移动文档到目录
// @Description  将一个或多个文档移动到指定目录（folder_id 空 = 移出所有目录到根层级）。单归属语义：文档的目录被整体替换。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id    path  string                      true  "知识库ID"
// @Param        move  body  types.DocFolderMoveRequest true  "移动目标"
// @Success      200
// @Failure      400  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/doc-folders/move [put]
func (h *DocFolderHandler) MoveDocuments(c *gin.Context) {
	kbID, _, err := h.validateDocFolderKB(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var req types.DocFolderMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	if len(req.KnowledgeIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge_ids is required"})
		return
	}
	if err := h.docFolderService.BatchMoveDocuments(c.Request.Context(), kbID, req.KnowledgeIDs, strings.TrimSpace(req.FolderID)); err != nil {
		writeDocFolderError(c, err)
		return
	}
	c.Status(http.StatusOK)
}

func writeDocFolderError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, repository.ErrDocFolderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case stderrors.Is(err, repository.ErrDocFolderConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
