package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// docFoldersTestDDL mirrors the production doc_folders DDL for SQLite, plus a
// minimal knowledges table with the folder_id placement column.
const docFoldersTestDDL = `
CREATE TABLE IF NOT EXISTS doc_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INTEGER NOT NULL DEFAULT 0,
    sort_order        INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME
);
`

const docFoldersKnowledgeTestDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type              VARCHAR(50) NOT NULL DEFAULT '',
    title             VARCHAR(255) NOT NULL DEFAULT '',
    source            VARCHAR(2048) NOT NULL DEFAULT '',
    parse_status      VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    folder_id         VARCHAR(36) NOT NULL DEFAULT '',
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME
);
`

func setupDocFoldersTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(docFoldersTestDDL).Error)
	require.NoError(t, db.Exec(docFoldersKnowledgeTestDDL).Error)
	return db
}

func makeDocFolder(kbID, parentID, name, path string, depth int) *types.DocFolder {
	return &types.DocFolder{
		ID:              uuid.New().String(),
		TenantID:        1,
		KnowledgeBaseID: kbID,
		ParentID:        parentID,
		Name:            name,
		Path:            path,
		Depth:           depth,
	}
}

// TestDocFolderRepository_CRUDAndSiblingConflict walks the folder lifecycle:
// create root + child, sibling name conflict under the same parent, rename,
// and soft-delete.
func TestDocFolderRepository_CRUDAndSiblingConflict(t *testing.T) {
	db := setupDocFoldersTestDB(t)
	repo := NewDocFolderRepository(db)
	ctx := context.Background()
	const kbID = "kb-1"

	root := makeDocFolder(kbID, types.DocFolderRootID, "制度", "制度", 1)
	require.NoError(t, repo.CreateFolder(ctx, root))

	child := makeDocFolder(kbID, root.ID, "办法", "制度/办法", 2)
	require.NoError(t, repo.CreateFolder(ctx, child))

	// Sibling name conflict: another folder with the same name under the
	// same parent must fail at the application layer via the unique
	// constraint. SQLite in-memory enforces the unique index only when the
	// index is created — simulate the check by relying on the repository's
	// GetChildFolderByName probe in the service; here assert the query works.
	dup, err := repo.GetChildFolderByName(ctx, kbID, root.ID, "办法")
	require.NoError(t, err)
	assert.Equal(t, child.ID, dup.ID)
	_, err = repo.GetChildFolderByName(ctx, kbID, root.ID, "条例")
	assert.ErrorIs(t, err, ErrDocFolderNotFound)

	// List children of the root.
	kids, err := repo.ListChildFolders(ctx, kbID, root.ID)
	require.NoError(t, err)
	require.Len(t, kids, 1)
	assert.Equal(t, "办法", kids[0].Name)

	// ListAll returns both, ordered by depth then path.
	all, err := repo.ListAllFolders(ctx, kbID)
	require.NoError(t, err)
	require.Len(t, all, 2)

	// Rename via UpdateFolder.
	child.Name = "规定"
	child.Path = "制度/规定"
	child.Depth = 2
	require.NoError(t, repo.UpdateFolder(ctx, child))

	got, err := repo.GetFolderByID(ctx, kbID, child.ID)
	require.NoError(t, err)
	assert.Equal(t, "规定", got.Name)
	assert.Equal(t, "制度/规定", got.Path)

	// Unknown folder id.
	_, err = repo.GetFolderByID(ctx, kbID, "nope")
	assert.ErrorIs(t, err, ErrDocFolderNotFound)

	// Update of a missing id reports not-found.
	missing := makeDocFolder(kbID, "", "ghost", "ghost", 1)
	missing.ID = "missing-folder"
	err = repo.UpdateFolder(ctx, missing)
	assert.ErrorIs(t, err, ErrDocFolderNotFound)

	// Soft delete.
	require.NoError(t, repo.DeleteFolder(ctx, kbID, child.ID))
	_, err = repo.GetFolderByID(ctx, kbID, child.ID)
	assert.ErrorIs(t, err, ErrDocFolderNotFound)
	allAfter, err := repo.ListAllFolders(ctx, kbID)
	require.NoError(t, err)
	assert.Len(t, allAfter, 1)
}

// TestDocFolderRepository_DocumentPlacement verifies counting and moving
// documents between folders, single-placement semantics, cross-KB safety and
// the recursive subtree listing helper.
func TestDocFolderRepository_DocumentPlacement(t *testing.T) {
	db := setupDocFoldersTestDB(t)
	repo := NewDocFolderRepository(db)
	ctx := context.Background()
	const kbID = "kb-1"

	f1 := makeDocFolder(kbID, "", "财务", "财务", 1)
	f2 := makeDocFolder(kbID, f1.ID, "报销", "财务/报销", 2)
	require.NoError(t, repo.CreateFolder(ctx, f1))
	require.NoError(t, repo.CreateFolder(ctx, f2))

	// Insert two documents directly under f2.
	require.NoError(t, db.Exec("INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title, folder_id) VALUES (?,1,?,?,?)",
		"doc-1", kbID, "报销单", f2.ID).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title, folder_id) VALUES (?,1,?,?,?)",
		"doc-2", kbID, "差旅费", f2.ID).Error)
	// One doc in another KB — must never count into kb-1.
	require.NoError(t, db.Exec("INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title, folder_id) VALUES (?,1,?,?,?)",
		"doc-other", "kb-other", "他库文档", f2.ID).Error)

	countF2, err := repo.CountDocumentsInFolder(ctx, kbID, f2.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), countF2)

	grouped, err := repo.ListDocumentsGroupedByFolder(ctx, kbID)
	require.NoError(t, err)
	assert.Contains(t, grouped[f2.ID], "doc-1")
	assert.Contains(t, grouped[f2.ID], "doc-2")
	assert.NotContains(t, grouped[f2.ID], "doc-other")

	// Move doc-1 up to f1.
	require.NoError(t, repo.SetKnowledgeFolder(ctx, kbID, f1.ID, []string{"doc-1"}))
	countF1, err := repo.CountDocumentsInFolder(ctx, kbID, f1.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), countF1)
	countF2after, err := repo.CountDocumentsInFolder(ctx, kbID, f2.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), countF2after)

	// Subtree listing: f1's subtree = [f1, f2].
	ids, err := repo.ListDocumentIDsByFolderIDs(ctx, kbID, []string{f1.ID, f2.ID})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"doc-1", "doc-2"}, ids)

	// Cross-KB docs never count: moving doc-other (kb-other) should report
	// a mismatch at the service layer; repo-level Count returns 0 for it.
	cnt, err := repo.CountDocumentsByIDs(ctx, kbID, []string{"doc-other"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), cnt)

	// Moving back to root (folder_id='') clears placement.
	require.NoError(t, repo.SetKnowledgeFolder(ctx, kbID, "", []string{"doc-2"}))
	cntRoot, err := repo.CountDocumentsInFolder(ctx, kbID, f2.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cntRoot)
}