package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDocFolderRepo is an in-memory DocFolderRepository implementable per
// test: it embeds the interface so unreferenced methods nil-panic (a signal
// that a test relies on a method the override set doesn't cover).
type fakeDocFolderRepo struct {
	interfaces.DocFolderRepository
	folders map[string]*types.DocFolder // by id
	docs    map[string]string           // doc id -> folder id
}

func newFakeDocFolderRepo() *fakeDocFolderRepo {
	return &fakeDocFolderRepo{
		folders: map[string]*types.DocFolder{},
		docs:    map[string]string{},
	}
}

func (f *fakeDocFolderRepo) CreateFolder(_ context.Context, folder *types.DocFolder) error {
	f.folders[folder.ID] = folder
	return nil
}

func (f *fakeDocFolderRepo) GetFolderByID(_ context.Context, kbID, id string) (*types.DocFolder, error) {
	if fb, ok := f.folders[id]; ok && fb.KnowledgeBaseID == kbID {
		return fb, nil
	}
	return nil, repository.ErrDocFolderNotFound
}

func (f *fakeDocFolderRepo) GetChildFolderByName(_ context.Context, kbID, parentID, name string) (*types.DocFolder, error) {
	for _, fb := range f.folders {
		if fb.KnowledgeBaseID == kbID && fb.ParentID == parentID && fb.Name == name {
			return fb, nil
		}
	}
	return nil, repository.ErrDocFolderNotFound
}

func (f *fakeDocFolderRepo) ListChildFolders(_ context.Context, kbID, parentID string) ([]*types.DocFolder, error) {
	var out []*types.DocFolder
	for _, fb := range f.folders {
		if fb.KnowledgeBaseID == kbID && fb.ParentID == parentID {
			out = append(out, fb)
		}
	}
	return out, nil
}

func (f *fakeDocFolderRepo) ListAllFolders(_ context.Context, kbID string) ([]*types.DocFolder, error) {
	var out []*types.DocFolder
	for _, fb := range f.folders {
		if fb.KnowledgeBaseID == kbID {
			out = append(out, fb)
		}
	}
	return out, nil
}

func (f *fakeDocFolderRepo) UpdateFolder(_ context.Context, folder *types.DocFolder) error {
	f.folders[folder.ID] = folder
	return nil
}

func (f *fakeDocFolderRepo) DeleteFolder(_ context.Context, kbID, id string) error {
	if _, ok := f.folders[id]; !ok {
		return repository.ErrDocFolderNotFound
	}
	delete(f.folders, id)
	return nil
}

func (f *fakeDocFolderRepo) CountDocumentsInFolder(_ context.Context, kbID, folderID string) (int64, error) {
	var n int64
	for _, fid := range f.docs {
		if fid == folderID {
			n++
		}
	}
	return n, nil
}

func (f *fakeDocFolderRepo) ListDocumentsGroupedByFolder(_ context.Context, kbID string) (map[string]map[string]struct{}, error) {
	out := map[string]map[string]struct{}{}
	for docID, fid := range f.docs {
		if fid == "" {
			continue
		}
		set := out[fid]
		if set == nil {
			set = map[string]struct{}{}
			out[fid] = set
		}
		set[docID] = struct{}{}
	}
	return out, nil
}

func (f *fakeDocFolderRepo) SetKnowledgeFolder(_ context.Context, kbID, folderID string, ids []string) error {
	for _, id := range ids {
		f.docs[id] = folderID
	}
	return nil
}

func (f *fakeDocFolderRepo) CountDocumentsByIDs(_ context.Context, kbID string, ids []string) (int64, error) {
	var n int64
	for _, id := range ids {
		if _, ok := f.docs[id]; ok {
			n++
		}
	}
	return n, nil
}

// seedFolderTree builds kb-1 with f1(财务) → f2(报销)/f3(预算), docs doc1/doc2
// under f2 and doc3 unfiled.
func seedFolderTree() *fakeDocFolderRepo {
	kbID := "kb-1"
	repo := newFakeDocFolderRepo()
	f1 := &types.DocFolder{ID: "f1", KnowledgeBaseID: kbID, ParentID: types.DocFolderRootID, Name: "财务", Path: "财务", Depth: 1}
	f2 := &types.DocFolder{ID: "f2", KnowledgeBaseID: kbID, ParentID: "f1", Name: "报销", Path: "财务/报销", Depth: 2}
	f3 := &types.DocFolder{ID: "f3", KnowledgeBaseID: kbID, ParentID: "f1", Name: "预算", Path: "财务/预算", Depth: 2}
	repo.folders = map[string]*types.DocFolder{"f1": f1, "f2": f2, "f3": f3}
	repo.docs = map[string]string{"doc1": "f2", "doc2": "f2", "doc3": ""}
	return repo
}

func TestDocFolderService_CreateFolder(t *testing.T) {
	ctx := context.Background()
	kbID := "kb-1"

	t.Run("rejects blank names", func(t *testing.T) {
		repo := newFakeDocFolderRepo()
		svc := NewDocFolderService(repo, nil)
		_, err := svc.CreateFolder(ctx, kbID, 1, types.DocFolderRootID, "   ")
		assert.Error(t, err)
	})

	t.Run("rejects path separators", func(t *testing.T) {
		repo := newFakeDocFolderRepo()
		svc := NewDocFolderService(repo, nil)
		_, err := svc.CreateFolder(ctx, kbID, 1, types.DocFolderRootID, "a/b")
		assert.Error(t, err)
	})

	t.Run("sibling name conflict", func(t *testing.T) {
		repo := newFakeDocFolderRepo()
		repo.folders["f1"] = &types.DocFolder{ID: "f1", KnowledgeBaseID: kbID, Name: "财务", Path: "财务", Depth: 1}
		svc := NewDocFolderService(repo, nil)
		_, err := svc.CreateFolder(ctx, kbID, 1, types.DocFolderRootID, "财务")
		assert.ErrorIs(t, err, repository.ErrDocFolderConflict)
	})

	t.Run("creates nested folder with materialized path", func(t *testing.T) {
		repo := newFakeDocFolderRepo()
		repo.folders["f1"] = &types.DocFolder{
			ID: "f1", KnowledgeBaseID: kbID, ParentID: types.DocFolderRootID,
			Name: "财务", Path: "财务", Depth: 1,
		}
		svc := NewDocFolderService(repo, nil)
		folder, err := svc.CreateFolder(ctx, kbID, 1, "f1", "报销")
		require.NoError(t, err)
		assert.Equal(t, "财务/报销", folder.Path)
		assert.Equal(t, 2, folder.Depth)
		assert.Equal(t, "f1", folder.ParentID)
	})
}

func TestDocFolderService_RenameOrMoveFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("cannot move into itself", func(t *testing.T) {
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		_, err := svc.RenameOrMoveFolder(ctx, "kb-1", "f2", "", "f2", true)
		assert.Error(t, err)
	})

	t.Run("cannot move into its own descendant", func(t *testing.T) {
		// f2 is a descendant of f1 → moving f1 under f2 is a cycle.
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		_, err := svc.RenameOrMoveFolder(ctx, "kb-1", "f1", "", "f2", true)
		assert.Error(t, err)
	})

	t.Run("sibling move is legal", func(t *testing.T) {
		// f2 (报销) and f3 (预算) are siblings under f1 → moving f2 under f3
		// is a valid reparent, not a cycle.
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		updated, err := svc.RenameOrMoveFolder(ctx, "kb-1", "f2", "", "f3", true)
		require.NoError(t, err)
		assert.Equal(t, "财务/预算/报销", updated.Path)
		assert.Equal(t, "f3", updated.ParentID)
	})

	t.Run("plain rename recomputes path", func(t *testing.T) {
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		updated, err := svc.RenameOrMoveFolder(ctx, "kb-1", "f2", "差旅", "", false)
		require.NoError(t, err)
		assert.Equal(t, "财务/差旅", updated.Path)
		// sibling path must be untouched
		assert.Equal(t, "财务/预算", repo.folders["f3"].Path)
	})

	t.Run("unknown folder", func(t *testing.T) {
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		_, err := svc.RenameOrMoveFolder(ctx, "kb-1", "nope", "x", "", false)
		assert.ErrorIs(t, err, repository.ErrDocFolderNotFound)
	})

	t.Run("rename conflicts with sibling", func(t *testing.T) {
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		_, err := svc.RenameOrMoveFolder(ctx, "kb-1", "f2", "预算", "", false)
		assert.ErrorIs(t, err, repository.ErrDocFolderConflict)
	})
}

func TestDocFolderService_DeleteFolder(t *testing.T) {
	ctx := context.Background()

	t.Run("blocks when it has child folders", func(t *testing.T) {
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		err := svc.DeleteFolder(ctx, "kb-1", "f1")
		assert.Error(t, err)
	})

	t.Run("blocks when it contains documents", func(t *testing.T) {
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		err := svc.DeleteFolder(ctx, "kb-1", "f2")
		assert.Error(t, err)
	})

	t.Run("deletes an empty folder", func(t *testing.T) {
		repo := seedFolderTree()
		svc := NewDocFolderService(repo, nil)
		require.NoError(t, svc.DeleteFolder(ctx, "kb-1", "f3"))
		_, err := repo.GetFolderByID(ctx, "kb-1", "f3")
		assert.ErrorIs(t, err, repository.ErrDocFolderNotFound)
	})
}

func TestDocFolderService_ExpandSubtree(t *testing.T) {
	repo := seedFolderTree()
	svc := NewDocFolderService(repo, nil)
	ctx := context.Background()

	ids, err := svc.ExpandSubtree(ctx, "kb-1", "f1")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"f1", "f2", "f3"}, ids)

	_, err = svc.ExpandSubtree(ctx, "kb-1", "nope")
	assert.ErrorIs(t, err, repository.ErrDocFolderNotFound)

	ids, err = svc.ExpandSubtree(ctx, "kb-1", types.DocFolderRootID)
	require.NoError(t, err)
	assert.Len(t, ids, 0)
}

func TestDocFolderService_MoveDocument(t *testing.T) {
	repo := seedFolderTree()
	svc := NewDocFolderService(repo, nil)
	ctx := context.Background()

	t.Run("moves into a folder", func(t *testing.T) {
		require.NoError(t, svc.MoveDocument(ctx, "kb-1", "doc3", "f1"))
		assert.Equal(t, "f1", repo.docs["doc3"])
	})

	t.Run("moves out to root", func(t *testing.T) {
		require.NoError(t, svc.MoveDocument(ctx, "kb-1", "doc1", types.DocFolderRootID))
		assert.Equal(t, "", repo.docs["doc1"])
	})

	t.Run("rejects unknown target folder", func(t *testing.T) {
		err := svc.MoveDocument(ctx, "kb-1", "doc1", "no-such-folder")
		assert.ErrorIs(t, err, repository.ErrDocFolderNotFound)
	})

	t.Run("rejects unknown document", func(t *testing.T) {
		err := svc.MoveDocument(ctx, "kb-1", "missing", "f1")
		assert.ErrorContains(t, err, "do not exist")
	})
}

func TestDocFolderService_ListFolders(t *testing.T) {
	repo := seedFolderTree()
	svc := NewDocFolderService(repo, nil)
	ctx := context.Background()

	nodes, err := svc.ListFolders(ctx, "kb-1", types.DocFolderRootID)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	root := nodes[0]
	assert.Equal(t, "财务", root.Name)
	// recursive doc count: f2 has doc1+doc2, f3 none → f1 subtree = 2
	assert.Equal(t, int64(2), root.DocCount)
	assert.True(t, root.HasChildren)
	require.Len(t, root.Folders, 2)
	// children sorted by name: 报销(f2, 2 docs), 预算(f3, 0)
	assert.Equal(t, "报销", root.Folders[0].Name)
	assert.Equal(t, int64(2), root.Folders[0].DocCount)
	assert.Equal(t, "预算", root.Folders[1].Name)
	assert.Equal(t, int64(0), root.Folders[1].DocCount)
}
