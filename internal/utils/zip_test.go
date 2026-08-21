package utils

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// createZipBytes builds a zip archive in memory with the given entries.
// Each entry is a name → content pair. Directories are created automatically
// based on path separators in the name.
func createZipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

// createNestedZipBytes builds a zip that contains another zip as an entry.
// outerEntries can reference the inner zip by the given innerName.
func createNestedZipBytes(t *testing.T, innerName string, innerEntries map[string]string, extraEntries map[string]string) []byte {
	t.Helper()
	innerData := createZipBytes(t, innerEntries)
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// Write the nested zip.
	fw, err := w.Create(innerName)
	if err != nil {
		t.Fatalf("create nested zip entry %s: %v", innerName, err)
	}
	if _, err := fw.Write(innerData); err != nil {
		t.Fatalf("write nested zip entry %s: %v", innerName, err)
	}
	// Write extra entries.
	for name, content := range extraEntries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

func TestExtractZipRecursively_NormalExtraction(t *testing.T) {
	entries := map[string]string{
		"doc1.pdf":  "pdf content",
		"doc2.txt":  "text content",
		"readme.md": "# Hello",
	}
	data := createZipBytes(t, entries)
	r := bytes.NewReader(data)

	allTypes := func(string) bool { return true }
	files, summary, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		IsTypeAllowed: allTypes,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
	if len(summary.SkippedTypes) != 0 || len(summary.SkippedOversize) != 0 || len(summary.SkippedDepth) != 0 || summary.SkippedCount != 0 {
		t.Fatalf("expected no skips, got %+v", summary)
	}
}

func TestExtractZipRecursively_NestedZip(t *testing.T) {
	innerEntries := map[string]string{
		"inner.md":  "inner markdown",
		"inner.txt": "inner text",
	}
	extraEntries := map[string]string{
		"outer.pdf": "outer pdf",
	}
	data := createNestedZipBytes(t, "nested.zip", innerEntries, extraEntries)
	r := bytes.NewReader(data)

	allTypes := func(string) bool { return true }
	files, summary, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		IsTypeAllowed: allTypes,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files (1 outer + 2 inner), got %d: %+v", len(files), files)
	}
	if len(summary.SkippedDepth) != 0 {
		t.Fatalf("expected no depth skips, got %v", summary.SkippedDepth)
	}

	// Verify inner files have the nested prefix.
	found := false
	for _, f := range files {
		if strings.HasSuffix(f.RelativePath, "inner.md") {
			found = true
			if !strings.Contains(f.RelativePath, "nested.zip/") {
				t.Errorf("expected nested prefix in relative path, got %s", f.RelativePath)
			}
		}
	}
	if !found {
		t.Error("inner.md not found in extracted files")
	}
}

func TestExtractZipRecursively_MaxDepthExceeded(t *testing.T) {
	// Build a chain of nested zips: outer.zip → level1.zip → level2.zip → deep.txt
	deepEntries := map[string]string{"deep.txt": "deepest content"}
	level2Data := createZipBytes(t, map[string]string{"level2.zip": string(createZipBytes(t, deepEntries))})
	level1Data := createZipBytes(t, map[string]string{"level1.zip": string(level2Data)})
	outerData := createZipBytes(t, map[string]string{"level1.zip": string(level1Data)})

	r := bytes.NewReader(outerData)
	allTypes := func(string) bool { return true }

	// MaxDepth=1 means only 1 level of nesting is allowed; level2.zip and deeper should be skipped.
	files, summary, err := ExtractZipRecursively(r, int64(len(outerData)), ExtractOptions{
		MaxDepth:          1,
		IsTypeAllowed:     allTypes,
		MaxTotalSizeBytes: 100 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// level1.zip is extracted at depth 0→1, but level2.zip inside it exceeds MaxDepth=1.
	if len(summary.SkippedDepth) == 0 {
		t.Error("expected depth-skipped entries, got none")
	}
	_ = files
}

func TestExtractZipRecursively_SkipUnsupportedTypes(t *testing.T) {
	entries := map[string]string{
		"good.pdf": "pdf content",
		"bad.exe":  "exe content",
		"bad.rar":  "rar content",
		"good.txt": "text content",
	}
	data := createZipBytes(t, entries)
	r := bytes.NewReader(data)

	allowed := func(name string) bool {
		lower := strings.ToLower(name)
		return strings.HasSuffix(lower, ".pdf") || strings.HasSuffix(lower, ".txt")
	}
	files, summary, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		IsTypeAllowed: allowed,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if len(summary.SkippedTypes) != 2 {
		t.Fatalf("expected 2 skipped types, got %d: %v", len(summary.SkippedTypes), summary.SkippedTypes)
	}
}

func TestExtractZipRecursively_MaxFilesExceeded(t *testing.T) {
	entries := make(map[string]string)
	for i := 0; i < 10; i++ {
		entries[fmt.Sprintf("file_%d.txt", i)] = "content"
	}
	data := createZipBytes(t, entries)
	r := bytes.NewReader(data)

	allTypes := func(string) bool { return true }
	files, summary, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		MaxFiles:          5,
		IsTypeAllowed:     allTypes,
		MaxTotalSizeBytes: 100 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 5 {
		t.Fatalf("expected 5 files, got %d", len(files))
	}
	if summary.SkippedCount != 5 {
		t.Fatalf("expected 5 skipped count, got %d", summary.SkippedCount)
	}
}

func TestExtractZipRecursively_PerFileSizeExceeded(t *testing.T) {
	entries := map[string]string{
		"small.txt": "small",
		"big.txt":   strings.Repeat("x", 1000),
	}
	data := createZipBytes(t, entries)
	r := bytes.NewReader(data)

	allTypes := func(string) bool { return true }
	files, summary, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		MaxFileSizeBytes:  500,
		IsTypeAllowed:     allTypes,
		MaxTotalSizeBytes: 100 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if len(summary.SkippedOversize) != 1 {
		t.Fatalf("expected 1 oversize skip, got %d", len(summary.SkippedOversize))
	}
}

func TestExtractZipRecursively_ZipSlipPrevention(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// Create a path-traversal entry.
	fw, err := w.Create("../../etc/passwd")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := fw.Write([]byte("root:x:0:0")); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	// Also add a valid file.
	fw2, err := w.Create("safe.txt")
	if err != nil {
		t.Fatalf("create safe entry: %v", err)
	}
	if _, err := fw2.Write([]byte("safe content")); err != nil {
		t.Fatalf("write safe entry: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	data := buf.Bytes()
	r := bytes.NewReader(data)
	allTypes := func(string) bool { return true }

	files, _, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		IsTypeAllowed: allTypes,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (path-traversal skipped), got %d", len(files))
	}
	if files[0].Name != "safe.txt" {
		t.Errorf("expected safe.txt, got %s", files[0].Name)
	}
}

func TestExtractZipRecursively_ZipBombRatioDetection(t *testing.T) {
	// Create a zip with content that will exceed the ratio limit.
	bigContent := strings.Repeat("a", 50*1024) // 50KB
	entries := map[string]string{
		"big1.txt": bigContent,
		"big2.txt": bigContent,
		"big3.txt": bigContent,
	}
	data := createZipBytes(t, entries)
	r := bytes.NewReader(data)

	allTypes := func(string) bool { return true }
	// Set a very tight ratio (1x) to trigger the bomb detection.
	_, _, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		MaxTotalSizeRatio: 1,
		MaxTotalSizeBytes: 1 << 30, // 1GB absolute cap
		IsTypeAllowed:     allTypes,
	})
	if err == nil {
		t.Fatal("expected zip bomb error, got nil")
	}
	if !strings.Contains(err.Error(), "zip bomb") {
		t.Errorf("expected zip bomb error, got: %v", err)
	}
}

func TestExtractZipRecursively_EmptyZip(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("close empty zip: %v", err)
	}
	data := buf.Bytes()
	r := bytes.NewReader(data)

	files, _, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		IsTypeAllowed: func(string) bool { return true },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files from empty zip, got %d", len(files))
	}
}

func TestDecodeZipEntryName_UTF8PassThrough(t *testing.T) {
	name := "report.pdf"
	got := decodeZipEntryName(name)
	if got != name {
		t.Errorf("expected UTF-8 name to pass through unchanged, got %q", got)
	}
}

func TestDecodeZipEntryName_GB18030Decode(t *testing.T) {
	// "济南市市监局文件" encoded as GB18030 (superset of GBK/GB2312).
	expected := "济南市市监局文件"
	gbkBytes := []byte{
		0xbc, 0xc3, 0xc4, 0xcf, 0xca, 0xd0, 0xca, 0xda,
		0xc2, 0xeb, 0xce, 0xc4, 0xbc, 0xfe, 0xb5, 0xc4,
		0xbc, 0xd2, 0xb5, 0xa5,
	}
	raw := string(gbkBytes)
	got := decodeZipEntryName(raw)
	if got != expected {
		t.Errorf("expected GB18030 decode to %q, got %q", expected, got)
	}
}

func TestDecodeZipEntryName_InvalidBytesFallback(t *testing.T) {
	// Random bytes that are neither valid UTF-8 nor valid GB18030.
	raw := string([]byte{0xff, 0xfe, 0x80, 0x81})
	got := decodeZipEntryName(raw)
	if got != raw {
		t.Errorf("expected invalid bytes to fall back unchanged, got %q", got)
	}
}

func TestExtractZipRecursively_GBKEncodedNames(t *testing.T) {
	// Build a zip whose entry names are raw GB18030 bytes.
	gbkBytes := []byte{
		0xbc, 0xc3, 0xc4, 0xcf, 0xca, 0xd0, 0xca, 0xda,
		0xc2, 0xeb, 0xce, 0xc4, 0xbc, 0xfe, 0xb5, 0xc4,
		0xbc, 0xd2, 0xb5, 0xa5,
	}
	entries := map[string]string{
		"utf8.pdf":                 "pdf content",
		string(gbkBytes) + ".docx": "docx content",
	}
	data := createZipBytes(t, entries)
	r := bytes.NewReader(data)

	allTypes := func(string) bool { return true }
	files, summary, err := ExtractZipRecursively(r, int64(len(data)), ExtractOptions{
		IsTypeAllowed: allTypes,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(files), files)
	}
	if len(summary.Failed) != 0 {
		t.Fatalf("expected no failed entries, got: %v", summary.Failed)
	}
}
