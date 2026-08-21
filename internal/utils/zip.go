package utils

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// ExtractOptions configures the behaviour of ExtractZipRecursively.
type ExtractOptions struct {
	// MaxDepth is the maximum nesting depth for recursive zip extraction.
	// A value of 0 means only the top-level zip is extracted (nested zips
	// are skipped). Default: 10.
	MaxDepth int
	// MaxFiles caps the total number of compliant files extracted across
	// all nesting levels. Files beyond this limit are recorded in
	// ExtractSummary.SkippedCount. Default: 500.
	MaxFiles int
	// MaxTotalSizeBytes is the absolute upper bound for the sum of all
	// extracted file sizes. Default: 1 GB.
	MaxTotalSizeBytes int64
	// MaxTotalSizeRatio guards against zip bombs: extraction stops when
	// the total extracted size exceeds MaxTotalSizeRatio * originalZipSize.
	// Default: 100.
	MaxTotalSizeRatio int64
	// IsTypeAllowed is called for each extracted entry; return false to
	// skip the file (recorded in ExtractSummary.SkippedTypes). If nil,
	// every non-zip entry is accepted.
	IsTypeAllowed func(filename string) bool
	// MaxFileSizeBytes is the per-file size limit; files exceeding this
	// are skipped and recorded in ExtractSummary.SkippedOversize. If 0,
	// no per-file limit is applied.
	MaxFileSizeBytes int64
}

// ExtractedFile represents a single file extracted from a zip archive.
type ExtractedFile struct {
	// Name is the sanitised base filename (e.g. "report.pdf").
	Name string
	// RelativePath preserves the directory structure inside the zip
	// (e.g. "docs/chapter1/report.pdf"). For nested zips the path
	// includes the parent zip name as a virtual directory.
	RelativePath string
	// Content holds the file bytes.
	Content []byte
	// Size is len(Content).
	Size int64
}

// ExtractSummary records files that were skipped during extraction.
type ExtractSummary struct {
	// SkippedTypes lists filenames skipped because their type was not
	// allowed by IsTypeAllowed (does NOT include nested zips which are
	// recursively extracted instead).
	SkippedTypes []string
	// SkippedOversize lists filenames skipped because they exceeded
	// MaxFileSizeBytes.
	SkippedOversize []string
	// SkippedDepth lists nested zip filenames that were not extracted
	// because the MaxDepth would have been exceeded.
	SkippedDepth []string
	// SkippedCount is the number of files skipped because MaxFiles was
	// reached.
	SkippedCount int
}

// extractState holds mutable counters shared across recursive calls.
type extractState struct {
	opts       ExtractOptions
	totalFiles int
	totalSize  int64
	zipSize    int64
	summary    ExtractSummary
}

// ExtractZipRecursively reads a zip archive from r (with total size zipSize)
// and returns all compliant files, recursively extracting nested zips up to
// opts.MaxDepth.
//
// It validates against ZipSlip path-traversal, zip bombs (size-ratio and
// absolute cap), file-count limits, per-file size limits, and type allowlists.
func ExtractZipRecursively(r io.ReaderAt, zipSize int64, opts ExtractOptions) ([]ExtractedFile, *ExtractSummary, error) {
	// Apply defaults.
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 10
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 500
	}
	if opts.MaxTotalSizeBytes <= 0 {
		opts.MaxTotalSizeBytes = 1 << 30 // 1 GB
	}
	if opts.MaxTotalSizeRatio <= 0 {
		opts.MaxTotalSizeRatio = 100
	}

	state := &extractState{
		opts:    opts,
		zipSize: zipSize,
	}
	var files []ExtractedFile
	if err := state.extract(r, zipSize, "", 0, &files); err != nil {
		return nil, &state.summary, err
	}
	return files, &state.summary, nil
}

// extract performs one level of zip extraction and recurses into nested zips.
func (s *extractState) extract(r io.ReaderAt, size int64, prefix string, depth int, out *[]ExtractedFile) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}

	for _, f := range zr.File {
		// Decode non-UTF-8 (typically GBK/GB18030 from Windows) entry names
		// before any validation, so Chinese filenames survive the pipeline.
		name := decodeZipEntryName(f.Name)

		// Skip directories.
		if f.FileInfo().IsDir() {
			continue
		}

		// ZipSlip: ensure the entry path does not escape the archive root.
		name = filepath.Clean(name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			// Silently skip path-traversal entries.
			continue
		}

		// Per-file size check.
		if s.opts.MaxFileSizeBytes > 0 && f.UncompressedSize64 > uint64(s.opts.MaxFileSizeBytes) {
			s.summary.SkippedOversize = append(s.summary.SkippedOversize, prefix+name)
			continue
		}

		// Check total extracted size so far (zip-bomb ratio + absolute cap).
		proposedSize := s.totalSize + int64(f.UncompressedSize64)
		if s.zipSize > 0 && proposedSize > s.opts.MaxTotalSizeRatio*s.zipSize {
			return fmt.Errorf("zip bomb detected: extracted size %d exceeds %d× original size %d",
				proposedSize, s.opts.MaxTotalSizeRatio, s.zipSize)
		}
		if proposedSize > s.opts.MaxTotalSizeBytes {
			return fmt.Errorf("zip bomb detected: extracted size %d exceeds absolute cap %d",
				proposedSize, s.opts.MaxTotalSizeBytes)
		}

		// File count check.
		if s.totalFiles >= s.opts.MaxFiles {
			s.summary.SkippedCount++
			continue
		}

		lowerName := strings.ToLower(name)
		relPath := prefix + name

		// Nested zip: recurse.
		if strings.HasSuffix(lowerName, ".zip") {
			if depth >= s.opts.MaxDepth {
				s.summary.SkippedDepth = append(s.summary.SkippedDepth, relPath)
				continue
			}
			data, err := readZipEntry(f)
			if err != nil {
				// Skip unreadable nested zips.
				continue
			}
			nestedPrefix := relPath + "/"
			nestedReader := bytes.NewReader(data)
			if err := s.extract(nestedReader, int64(len(data)), nestedPrefix, depth+1, out); err != nil {
				// Log but continue — a bad nested zip shouldn't abort the whole extraction.
				continue
			}
			continue
		}

		// Type allowlist check.
		if s.opts.IsTypeAllowed != nil && !s.opts.IsTypeAllowed(name) {
			s.summary.SkippedTypes = append(s.summary.SkippedTypes, relPath)
			continue
		}

		// Read file content.
		data, err := readZipEntry(f)
		if err != nil {
			continue
		}

		// Update state counters.
		s.totalFiles++
		s.totalSize += int64(len(data))

		baseName := filepath.Base(name)
		*out = append(*out, ExtractedFile{
			Name:         baseName,
			RelativePath: relPath,
			Content:      data,
			Size:         int64(len(data)),
		})
	}
	return nil
}

// readZipEntry reads the full content of a single zip file entry.
func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read zip entry %s: %w", f.Name, err)
	}
	return data, nil
}

// decodeZipEntryName decodes a zip entry name into a UTF-8 string.
//
// Many ZIP archives created on Windows use GBK/GB18030 for Chinese filenames
// instead of UTF-8. Go's archive/zip preserves the raw bytes in File.Name, so
// those names fail UTF-8 validation downstream and get rejected as "illegal
// characters". This helper attempts a lossless conversion without disturbing
// names that are already valid UTF-8.
//
// Order:
//  1. If the raw bytes are already valid UTF-8, return as-is.
//  2. Try GB18030 decode (superset of GBK/GB2312) into UTF-8.
//  3. On decode failure, fall back to the raw bytes string so the caller can
//     still decide how to handle it.
func decodeZipEntryName(raw string) string {
	if utf8.ValidString(raw) {
		return raw
	}
	decoded, _, err := transform.String(
		simplifiedchinese.GB18030.NewDecoder(),
		raw,
	)
	if err == nil {
		return decoded
	}
	return raw
}
