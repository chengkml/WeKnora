package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeKeywordRowsRepo provides canned frequent_keyword rows for the overview.
// It embeds the full interface so it satisfies WikiPageRepository; any other
// method call would nil-panic, which is the signal we want in these tests.
type fakeKeywordRowsRepo struct {
	interfaces.WikiPageRepository
	rows []types.WikiKeywordRow
}

func (f *fakeKeywordRowsRepo) ListFrequentKeywordRows(_ context.Context, _ string) ([]types.WikiKeywordRow, error) {
	return f.rows, nil
}

func TestParseWikiKeywordHead(t *testing.T) {
	cases := []struct {
		name     string
		head     string
		wantDocs int
		wantFreq int
	}{
		{
			name:     "canonical opening line",
			head:     "# 记录\n\n本关键词在 5 篇文档中高频出现（总频次：73）。\n\n## 原文出现位置",
			wantDocs: 5,
			wantFreq: 73,
		},
		{
			name:     "canonical with whitespace variation",
			head:     "本关键词在 12 篇文档中高频出现（总频次： 340 ）。",
			wantDocs: 12,
			wantFreq: 340,
		},
		{
			name:     "old single-table format falls back to per-doc sums",
			head:     "# 医疗器械\n\n本关键词高频出现。\n\n**《A.docx》**（频次：21）\n**《B.docx》**（频次：9）",
			wantDocs: 1,
			wantFreq: 30,
		},
		{
			name:     "unparseable head yields zero frequency",
			head:     "# 噪声\n\n没有任何可解析的数字。",
			wantDocs: 1,
			wantFreq: 0,
		},
		{
			name:     "empty head",
			head:     "",
			wantDocs: 1,
			wantFreq: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docs, freq := parseWikiKeywordHead(tc.head)
			if docs != tc.wantDocs || freq != tc.wantFreq {
				t.Errorf("parseWikiKeywordHead() = (%d, %d), want (%d, %d)", docs, freq, tc.wantDocs, tc.wantFreq)
			}
		})
	}
}

func keywordTestRows() []types.WikiKeywordRow {
	rows := []struct {
		slug, title, head string
	}{
		{"kw/a", "优先审评审批", "本关键词在 8 篇文档中高频出现（总频次：120）。"},
		{"kw/b", "记录", "本关键词在 5 篇文档中高频出现（总频次：73）。"},
		{"kw/c", "医疗器械经营", "本关键词在 3 篇文档中高频出现（总频次：45）。"},
		{"kw/d", "行政", "本关键词在 1 篇文档中高频出现（总频次：12）。"},
		{"kw/e", "未解析词", "# 未解析词\n\n没有数字。"},
	}
	out := make([]types.WikiKeywordRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, types.WikiKeywordRow{Slug: r.slug, Title: r.title, ContentHead: r.head})
	}
	return out
}

func TestListKeywordOverviewDefaultDesc(t *testing.T) {
	svc := &wikiPageService{repo: &fakeKeywordRowsRepo{rows: keywordTestRows()}}
	res, err := svc.ListKeywordOverview(context.Background(), "kb1", "", "freq", "desc", 1, 50)
	if err != nil {
		t.Fatalf("ListKeywordOverview() error: %v", err)
	}
	if res.Total != 5 {
		t.Fatalf("Total = %d, want 5", res.Total)
	}
	want := []struct {
		kw   string
		freq int
	}{
		{"优先审评审批", 120},
		{"记录", 73},
		{"医疗器械经营", 45},
		{"行政", 12},
		{"未解析词", 0},
	}
	if len(res.Items) != len(want) {
		t.Fatalf("len(Items) = %d, want %d", len(res.Items), len(want))
	}
	for i, w := range want {
		it := res.Items[i]
		if it.Keyword != w.kw || it.TotalFreq != w.freq {
			t.Errorf("Items[%d] = %s@%d, want %s@%d", i, it.Keyword, it.TotalFreq, w.kw, w.freq)
		}
	}
}

func TestListKeywordOverviewSearchAndPagination(t *testing.T) {
	svc := &wikiPageService{repo: &fakeKeywordRowsRepo{rows: keywordTestRows()}}

	// search narrows the set
	res, err := svc.ListKeywordOverview(context.Background(), "kb1", "记录", "freq", "desc", 1, 50)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if res.Total != 1 || res.Items[0].Keyword != "记录" {
		t.Errorf("search '记录' -> total %d, first %s; want 1 / 记录", res.Total, res.Items[0].Keyword)
	}

	// pagination slices
	res, err = svc.ListKeywordOverview(context.Background(), "kb1", "", "freq", "desc", 2, 2)
	if err != nil {
		t.Fatalf("pagination error: %v", err)
	}
	if res.Total != 5 || len(res.Items) != 2 || res.Items[0].Keyword != "医疗器械经营" {
		t.Errorf("page2: total %d len %d first %s; want 5/2/医疗器械经营", res.Total, len(res.Items), res.Items[0].Keyword)
	}

	// out-of-range page returns empty items but the true total
	res, err = svc.ListKeywordOverview(context.Background(), "kb1", "", "freq", "desc", 99, 50)
	if err != nil {
		t.Fatalf("out-of-range error: %v", err)
	}
	if res.Total != 5 || len(res.Items) != 0 {
		t.Errorf("page99: total %d len %d; want 5/0", res.Total, len(res.Items))
	}
}

func TestListKeywordOverviewSortVariants(t *testing.T) {
	svc := &wikiPageService{repo: &fakeKeywordRowsRepo{rows: keywordTestRows()}}

	// freq ascending — the zero-freq page leads but its doc_count tie-break
	// (both 1) falls through to keyword ascending
	res, err := svc.ListKeywordOverview(context.Background(), "kb1", "", "freq", "asc", 1, 50)
	if err != nil {
		t.Fatalf("asc error: %v", err)
	}
	if res.Items[0].Keyword != "未解析词" || res.Items[len(res.Items)-1].Keyword != "优先审评审批" {
		t.Errorf("freq asc first/last = %s/%s, want 未解析词/优先审评审批",
			res.Items[0].Keyword, res.Items[len(res.Items)-1].Keyword)
	}

	// keyword sort
	res, err = svc.ListKeywordOverview(context.Background(), "kb1", "", "keyword", "desc", 1, 50)
	if err != nil {
		t.Fatalf("keyword sort error: %v", err)
	}
	// Chinese ordering in Go's byte-wise comparison is not meaningful, but the
	// sort must be deterministic and cover all rows
	if len(res.Items) != 5 {
		t.Errorf("keyword sort len = %d, want 5", len(res.Items))
	}
}
