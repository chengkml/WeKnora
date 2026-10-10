package types

import (
	"time"

	"gorm.io/gorm"
)

// DocFolderRootID is the sentinel parent/folder id meaning "the KB root"
// (a document or folder directly under the top level, with no parent folder).
const DocFolderRootID = ""

// DocFolder is a first-class directory node for document classification.
// One document belongs to exactly one folder (knowledges.folder_id, ” = root);
// parent_id forms an adjacency-list tree (” = root). Path is the materialized
// "/"-joined name chain kept for cheap display/sort. Mirrors WikiFolder.
type DocFolder struct {
	ID              string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64         `json:"tenant_id" gorm:"index"`
	KnowledgeBaseID string         `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	ParentID        string         `json:"parent_id" gorm:"column:parent_id;type:varchar(36);index;default:''"`
	Name            string         `json:"name" gorm:"type:varchar(255)"`
	Path            string         `json:"path" gorm:"type:varchar(1024)"`
	Depth           int            `json:"depth" gorm:"default:0"`
	SortOrder       int            `json:"sort_order" gorm:"default:0"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName specifies the database table name
func (DocFolder) TableName() string {
	return "doc_folders"
}

// DocFolderNode is one directory node returned to the browser, enriched with
// the recursive document count (whole subtree) and whether it has child
// folders (for the expand arrow).
type DocFolderNode struct {
	DocFolder
	DocCount    int64           `json:"doc_count"`
	HasChildren bool            `json:"has_children"`
	Folders     []DocFolderNode `json:"folders,omitempty"`
}

// DocFolderListResponse is the payload for listing the directory tree under a
// parent folder (parent_id="" = root level).
type DocFolderListResponse struct {
	ParentID string          `json:"parent_id"`
	Folders  []DocFolderNode `json:"folders"`
}

// DocFolderCreateRequest creates a new (initially empty) folder under ParentID.
type DocFolderCreateRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
}

// DocFolderUpdateRequest renames and/or reparents a folder. ParentID is
// applied only when MoveParent is true so a pure rename doesn't have to
// re-send the (possibly root "") parent and risk an accidental move.
type DocFolderUpdateRequest struct {
	Name       string `json:"name,omitempty"`
	ParentID   string `json:"parent_id,omitempty"`
	MoveParent bool   `json:"move_parent,omitempty"`
}

// DocFolderMoveRequest relocates one or more documents into FolderID ("" = KB
// root, i.e. moved out of any folder). Single-placement semantics: the
// document's folder_id is replaced.
type DocFolderMoveRequest struct {
	FolderID     string   `json:"folder_id"`
	KnowledgeIDs []string `json:"knowledge_ids"`
}
