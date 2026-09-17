package service

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestExportSkillRoundTrip 验证「怎么导出就能怎么导入」:
// ExportSkill 打出的 ZIP → UploadSkill 能直接安装且结构一致。
func TestExportSkillRoundTrip(t *testing.T) {
	src := t.TempDir()
	skillDir := filepath.Join(src, "demo-skill")
	mustWriteFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: demo-skill\ndescription: Demo skill for round-trip test\n---\n# Demo skill\n")
	mustWriteFile(t, filepath.Join(skillDir, "scripts", "run.py"), "print('ok')\n")

	svc := &skillService{preloadedDir: src, initialized: false}
	data, err := svc.ExportSkill(t.Context(), "demo-skill")
	if err != nil {
		t.Fatalf("ExportSkill: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("导出 ZIP 为空")
	}

	// 1) ZIP 内应为 <技能名>/<文件> 布局(与导入 strip 前缀的格式一致)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("导出内容不是合法 ZIP: %v", err)
	}
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	for _, want := range []string{"demo-skill/SKILL.md", "demo-skill/scripts/run.py"} {
		if !got[want] {
			t.Fatalf("导出 ZIP 缺少条目 %q,实际: %v", want, got)
		}
	}

	// 2) 用导出的 ZIP 直接导入到全新目录
	dst := t.TempDir()
	svc2 := &skillService{preloadedDir: dst, initialized: false}
	meta, err := svc2.UploadSkill(t.Context(), data, "")
	if err != nil {
		t.Fatalf("导出 ZIP 无法重新导入: %v", err)
	}
	if meta.Name != "demo-skill" {
		t.Fatalf("导入后技能名 = %q,期望 demo-skill", meta.Name)
	}
	restored := filepath.Join(dst, "demo-skill", "scripts", "run.py")
	rc, err := os.Open(restored)
	if err != nil {
		t.Fatalf("重新导入后脚本缺失: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != "print('ok')\n" {
		t.Fatalf("重新导入后文件内容不一致: %q", string(body))
	}
}