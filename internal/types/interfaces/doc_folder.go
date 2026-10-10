package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// DocFolderService defines high-level operations on the multi-level document
// directory tree (doc_folders). Design (2026-10-10): a document belongs to
// exactly one folder; a parent folder's view recursively includes its whole
// subtree; folders coexist with the flat tag system; non-empty folders cannot
// be deleted; all document types (including FAQ) participate.
type DocFolderService interface {
	// ListFolders returns the directory tree under parentID ("" = root) as
	// nested nodes, each enriched with a recursive document count (whole
	// subtree) and whether it has child folders.
	ListFolders(ctx context.Context, kbID string, parentID string) ([]types.DocFolderNode, error)
	// GetFolder retrieves a single folder by id.
	GetFolder(ctx context.Context, kbID string, id string) (*types.DocFolder, error)
	// CreateFolder creates a new (initially empty) folder under parentID and
	// returns it. Fails if a sibling with the same name already exists.
	CreateFolder(ctx context.Context, kbID string, tenantID uint64, parentID string, name string) (*types.DocFolder, error)
	// RenameOrMoveFolder renames and/or reparents a folder, recomputing the
	// materialized path/depth of the whole subtree. Returns the updated folder.
	RenameOrMoveFolder(ctx context.Context, kbID string, id string, newName string, newParentID string, moveParent bool) (*types.DocFolder, error)
	// DeleteFolder removes an empty folder. Fails if it still contains
	// documents or child folders (the UI must relocate contents first).
	DeleteFolder(ctx context.Context, kbID string, id string) error
	// ExpandSubtree returns id plus every descendant folder id (the folder's
	// whole subtree set). "" returns nothing. Used by the list filter for
	// recursive folder views.
	ExpandSubtree(ctx context.Context, kbID string, id string) ([]string, error)
	// MoveDocument relocates a single document into folderID ("" = KB root).
	// The folder must belong to the same KB; the document must exist and
	// belong to the KB.
	MoveDocument(ctx context.Context, kbID string, knowledgeID string, folderID string) error
	// BatchMoveDocuments relocates one or more documents into folderID ("" =
	// KB root). All knowledge ids must be live and belong to kbID (any miss
	// aborts the batch, no partial moves); folderID must exist in kbID's
	// folder tree. Single-placement semantics.
	BatchMoveDocuments(ctx context.Context, kbID string, knowledgeIDs []string, folderID string) error
}

// DocFolderRepository defines persistence for the document directory tree.
type DocFolderRepository interface {
	// CreateFolder inserts a new directory node.
	CreateFolder(ctx context.Context, folder *types.DocFolder) error
	// GetFolderByID retrieves a folder by id within a knowledge base.
	GetFolderByID(ctx context.Context, kbID string, id string) (*types.DocFolder, error)
	// GetChildFolderByName returns the live child folder of parentID with the
	// given name, or ErrDocFolderNotFound.
	GetChildFolderByName(ctx context.Context, kbID string, parentID string, name string) (*types.DocFolder, error)
	// ListChildFolders returns the direct child folders of parentID, ordered
	// by sort_order then name.
	ListChildFolders(ctx context.Context, kbID string, parentID string) ([]*types.DocFolder, error)
	// ListAllFolders returns every folder in the knowledge base.
	ListAllFolders(ctx context.Context, kbID string) ([]*types.DocFolder, error)
	// UpdateFolder rewrites a folder's mutable fields (name, parent_id, path,
	// depth, sort_order, updated_at).
	UpdateFolder(ctx context.Context, folder *types.DocFolder) error
	// DeleteFolder soft-deletes a folder by id.
	DeleteFolder(ctx context.Context, kbID string, id string) error
	// CountDocumentsInFolder returns the number of live documents directly
	// filed under the folder.
	CountDocumentsInFolder(ctx context.Context, kbID string, folderID string) (int64, error)
	// ListDocumentsGroupedByFolder returns the live document ids directly
	// filed under each folder, keyed by folder_id. Used to compute recursive
	// document counts for the directory tree.
	ListDocumentsGroupedByFolder(ctx context.Context, kbID string) (map[string]map[string]struct{}, error)
	// ListDocumentIDsByFolderIDs returns live document ids filed under any of
	// the given folders (the expanded subtree set).
	ListDocumentIDsByFolderIDs(ctx context.Context, kbID string, folderIDs []string) ([]string, error)
	// SetKnowledgeFolder moves the given live documents (all verified to
	// belong to kbID) into the given folder ("" = KB root).
	SetKnowledgeFolder(ctx context.Context, kbID string, folderID string, knowledgeIDs []string) error
	// CountDocumentsByIDs returns how many of the given knowledge ids are live
	// and belong to kbID. Used by MoveDocument to fail fast when a target does
	// not belong to the KB before mutating any row.
	CountDocumentsByIDs(ctx context.Context, kbID string, knowledgeIDs []string) (int64, error)
}
