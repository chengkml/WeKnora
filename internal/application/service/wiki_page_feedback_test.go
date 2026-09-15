package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeFeedbackRepo embeds the full WikiPageRepository interface so tests only
// stub the two methods CreateFeedback touches (GetBySlug, CreateFeedback);
// any other method call would nil-panic, which is exactly the signal we want.
type fakeFeedbackRepo struct {
	interfaces.WikiPageRepository
	getBySlug      func(ctx context.Context, kbID string, slug string) (*types.WikiPage, error)
	createFeedback func(ctx context.Context, feedback *types.WikiPageFeedback) error
}

func (f *fakeFeedbackRepo) GetBySlug(ctx context.Context, kbID string, slug string) (*types.WikiPage, error) {
	return f.getBySlug(ctx, kbID, slug)
}

func (f *fakeFeedbackRepo) CreateFeedback(ctx context.Context, feedback *types.WikiPageFeedback) error {
	return f.createFeedback(ctx, feedback)
}

func newFeedbackTestSvc(t *testing.T, page *types.WikiPage, pageErr error, capture *types.WikiPageFeedback) *wikiPageService {
	t.Helper()
	repo := &fakeFeedbackRepo{
		getBySlug: func(_ context.Context, _ string, _ string) (*types.WikiPage, error) {
			return page, pageErr
		},
		createFeedback: func(_ context.Context, fb *types.WikiPageFeedback) error {
			*capture = *fb
			return nil
		},
	}
	return &wikiPageService{repo: repo}
}

func TestCreateFeedbackPreset(t *testing.T) {
	cases := []struct {
		name       string
		input      func() *types.WikiPageFeedback
		wantErr    string
		wantType   string
		wantPreset string
		wantEmpty  bool
	}{
		{
			name: "preset without content auto-fills canonical label and defaults to question",
			input: func() *types.WikiPageFeedback {
				return &types.WikiPageFeedback{KnowledgeBaseID: "kb1", Slug: "entity/acme", Preset: "content_error"}
			},
			wantType:   string(types.WikiFeedbackQuestion),
			wantPreset: "content_error",
		},
		{
			name: "preset with explicit content keeps the typed content",
			input: func() *types.WikiPageFeedback {
				return &types.WikiPageFeedback{KnowledgeBaseID: "kb1", Slug: "entity/acme", Preset: "broken_link", Content: "第二段的外部链接 404 了"}
			},
			wantType:   string(types.WikiFeedbackQuestion),
			wantPreset: "broken_link",
		},
		{
			name: "preset with explicit comment type is respected",
			input: func() *types.WikiPageFeedback {
				return &types.WikiPageFeedback{
					KnowledgeBaseID: "kb1", Slug: "entity/acme", Preset: "outdated",
					FeedbackType: string(types.WikiFeedbackComment),
				}
			},
			wantType:   string(types.WikiFeedbackComment),
			wantPreset: "outdated",
		},
		{
			name: "unknown preset key is rejected",
			input: func() *types.WikiPageFeedback {
				return &types.WikiPageFeedback{KnowledgeBaseID: "kb1", Slug: "entity/acme", Preset: "not-a-preset"}
			},
			wantErr: "invalid preset",
		},
		{
			name: "no preset and empty content is rejected",
			input: func() *types.WikiPageFeedback {
				return &types.WikiPageFeedback{KnowledgeBaseID: "kb1", Slug: "entity/acme"}
			},
			wantErr: "feedback content is required",
		},
		{
			name: "typed comment without preset still works",
			input: func() *types.WikiPageFeedback {
				return &types.WikiPageFeedback{KnowledgeBaseID: "kb1", Slug: "entity/acme", Content: "随便写点"}
			},
			wantType: string(types.WikiFeedbackComment),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured types.WikiPageFeedback
			svc := newFeedbackTestSvc(t,
				&types.WikiPage{Title: "Acme 实体"}, nil, &captured,
			)

			out, err := svc.CreateFeedback(context.Background(), tc.input())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("CreateFeedback() error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateFeedback() unexpected error: %v", err)
			}
			if out.Preset != tc.wantPreset {
				t.Errorf("preset = %q, want %q", out.Preset, tc.wantPreset)
			}
			if tc.wantType != "" && out.FeedbackType != tc.wantType {
				t.Errorf("feedback_type = %q, want %q", out.FeedbackType, tc.wantType)
			}
			if tc.wantEmpty {
				if strings.TrimSpace(out.Content) != "" {
					t.Errorf("content = %q, want empty", out.Content)
				}
			} else if strings.TrimSpace(out.Content) == "" {
				t.Errorf("content is empty, want auto-filled label or typed content")
			}
			if out.PageTitle == "" {
				t.Errorf("page_title should be denormalized from GetBySlug")
			}
			if out.Status != string(types.WikiFeedbackPending) {
				t.Errorf("status = %q, want pending default", out.Status)
			}
			if out.ID == "" {
				t.Errorf("id should be generated")
			}
		})
	}
}

func TestCreateFeedbackPresetLabelConsistency(t *testing.T) {
	// Every preset key must carry a non-empty Chinese label used to auto-fill
	// content; the frontend dropdown keys must keep in sync with this map.
	for key, label := range types.WikiFeedbackPresetKeys {
		if strings.TrimSpace(key) == "" {
			t.Errorf("preset key is empty")
		}
		if strings.TrimSpace(label) == "" {
			t.Errorf("preset %q has empty label", key)
		}
	}
	if len(types.WikiFeedbackPresetKeys) < 3 {
		t.Errorf("expected at least 3 presets, got %d", len(types.WikiFeedbackPresetKeys))
	}
}

func TestFeedbackPresetKeysListSorted(t *testing.T) {
	got := feedbackPresetKeysList()
	parts := strings.Split(got, ", ")
	if len(parts) != len(types.WikiFeedbackPresetKeys) {
		t.Fatalf("feedbackPresetKeysList() = %q, want %d keys", got, len(types.WikiFeedbackPresetKeys))
	}
	for i := 1; i < len(parts); i++ {
		if parts[i-1] >= parts[i] {
			t.Fatalf("feedbackPresetKeysList() not sorted: %q before %q", parts[i-1], parts[i])
		}
	}
}

func TestCreateFeedbackMissingPageTolerated(t *testing.T) {
	// A missing/soft-deleted page must not fail the write; the slug is used
	// as the fallback page title.
	repo := &fakeFeedbackRepo{
		getBySlug: func(_ context.Context, _ string, _ string) (*types.WikiPage, error) {
			return nil, errors.New("not found")
		},
		createFeedback: func(_ context.Context, _ *types.WikiPageFeedback) error {
			return nil
		},
	}
	svc := &wikiPageService{repo: repo}
	out, err := svc.CreateFeedback(context.Background(), &types.WikiPageFeedback{
		KnowledgeBaseID: "kb1", Slug: "entity/archived", Preset: "incomplete",
	})
	if err != nil {
		t.Fatalf("CreateFeedback() error = %v", err)
	}
	if out.PageTitle != "entity/archived" {
		t.Errorf("page_title = %q, want slug fallback", out.PageTitle)
	}
	if out.Content != "信息不完整" {
		t.Errorf("content = %q, want auto-filled label %q", out.Content, "信息不完整")
	}
}
