package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// ErrDocFolderNotFound is returned when a document folder is not found.
var ErrDocFolderNotFound = errors.New("doc folder not found")

// ErrDocFolderConflict is returned when a sibling folder with the same name
// already exists under the same parent.
var ErrDocFolderConflict = errors.New("doc folder name conflict")

// docFolderRepository persists the multi-level document directory tree
// (doc_folders). One document belongs to exactly one folder, stored as
// knowledges.folder_id. Mirrors the wiki folder repository.
type docFolderRepository struct {
	db *gorm.DB
}

// NewDocFolderRepository creates a doc folder repository.
func NewDocFolderRepository(db *gorm.DB) interfaces.DocFolderRepository {
	return &docFolderRepository{db: db}
}

func (r *docFolderRepository) CreateFolder(ctx context.Context, folder *types.DocFolder) error {
	return r.db.WithContext(ctx).Create(folder).Error
}

func (r *docFolderRepository) GetFolderByID(ctx context.Context, kbID string, id string) (*types.DocFolder, error) {
	var folder types.DocFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND id = ?", kbID, id).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDocFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *docFolderRepository) GetChildFolderByName(
	ctx context.Context, kbID string, parentID string, name string,
) (*types.DocFolder, error) {
	var folder types.DocFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND parent_id = ? AND name = ?", kbID, parentID, name).
		First(&folder).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDocFolderNotFound
		}
		return nil, err
	}
	return &folder, nil
}

func (r *docFolderRepository) ListChildFolders(
	ctx context.Context, kbID string, parentID string,
) ([]*types.DocFolder, error) {
	var folders []*types.DocFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND parent_id = ?", kbID, parentID).
		Order("sort_order ASC").
		Order("name ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *docFolderRepository) ListAllFolders(ctx context.Context, kbID string) ([]*types.DocFolder, error) {
	var folders []*types.DocFolder
	if err := r.db.WithContext(ctx).
		Where("knowledge_base_id = ?", kbID).
		Order("depth ASC").
		Order("path ASC").
		Find(&folders).Error; err != nil {
		return nil, err
	}
	return folders, nil
}

func (r *docFolderRepository) UpdateFolder(ctx context.Context, folder *types.DocFolder) error {
	result := r.db.WithContext(ctx).
		Model(&types.DocFolder{}).
		Where("id = ?", folder.ID).
		Updates(map[string]interface{}{
			"parent_id":  folder.ParentID,
			"name":       folder.Name,
			"path":       folder.Path,
			"depth":      folder.Depth,
			"sort_order": folder.SortOrder,
			"updated_at": folder.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDocFolderNotFound
	}
	return nil
}

func (r *docFolderRepository) DeleteFolder(ctx context.Context, kbID string, id string) error {
	result := r.db.WithContext(ctx).
		Where("knowledge_base_id = ? AND id = ?", kbID, id).
		Delete(&types.DocFolder{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDocFolderNotFound
	}
	return nil
}

// CountDocumentsInFolder returns the number of live documents directly filed
// under the folder (folder_id = id). Subtree counting happens in the service
// via ListDocumentsGroupedByFolder.
func (r *docFolderRepository) CountDocumentsInFolder(ctx context.Context, kbID string, folderID string) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledge_base_id = ? AND folder_id = ?", kbID, folderID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// ListDocumentsGroupedByFolder returns the live document ids directly filed
// under each folder, keyed by folder_id. Used by the directory tree to compute
// recursive document counts.
func (r *docFolderRepository) ListDocumentsGroupedByFolder(
	ctx context.Context, kbID string,
) (map[string]map[string]struct{}, error) {
	type row struct {
		FolderID string
		ID       string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("knowledges").
		Select("folder_id, id").
		Where("knowledge_base_id = ? AND folder_id <> '' AND deleted_at IS NULL", kbID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]map[string]struct{})
	for _, rw := range rows {
		set := out[rw.FolderID]
		if set == nil {
			set = make(map[string]struct{})
			out[rw.FolderID] = set
		}
		set[rw.ID] = struct{}{}
	}
	return out, nil
}

// ListDocumentIDsByFolderIDs returns live document ids filed under any of the
// given folders (the expanded subtree set).
func (r *docFolderRepository) ListDocumentIDsByFolderIDs(
	ctx context.Context, kbID string, folderIDs []string,
) ([]string, error) {
	if len(folderIDs) == 0 {
		return nil, nil
	}
	var ids []string
	if err := r.db.WithContext(ctx).
		Table("knowledges").
		Select("id").
		Where("knowledge_base_id = ? AND folder_id IN ? AND deleted_at IS NULL", kbID, folderIDs).
		Scan(&ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// SetKnowledgeFolder moves one or more live documents (all verified to belong
// to kbID) into the given folder ("" = KB root, i.e. moved out of any folder).
func (r *docFolderRepository) SetKnowledgeFolder(
	ctx context.Context, kbID string, folderID string, knowledgeIDs []string,
) error {
	if len(knowledgeIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledge_base_id = ? AND id IN ? AND deleted_at IS NULL", kbID, knowledgeIDs).
		Update("folder_id", folderID).Error
}

// CountDocumentsByIDs returns how many of the given knowledge ids are live and
// belong to kbID. Used by MoveDocument to fail fast when a target does not
// belong to the KB before mutating any row.
func (r *docFolderRepository) CountDocumentsByIDs(
	ctx context.Context, kbID string, knowledgeIDs []string,
) (int64, error) {
	if len(knowledgeIDs) == 0 {
		return 0, nil
	}
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&types.Knowledge{}).
		Where("knowledge_base_id = ? AND id IN ? AND deleted_at IS NULL", kbID, knowledgeIDs).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
