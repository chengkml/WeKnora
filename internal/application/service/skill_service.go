package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// DefaultPreloadedSkillsDir is the default directory for preloaded skills
const DefaultPreloadedSkillsDir = "skills/preloaded"

// skillService implements SkillService interface
type skillService struct {
	loader       *skills.Loader
	preloadedDir string
	mu           sync.RWMutex
	initialized  bool
}

// NewSkillService creates a new skill service
func NewSkillService() interfaces.SkillService {
	// Determine the preloaded skills directory
	preloadedDir := getPreloadedSkillsDir()

	return &skillService{
		preloadedDir: preloadedDir,
		initialized:  false,
	}
}

// getPreloadedSkillsDir returns the path to the preloaded skills directory
func getPreloadedSkillsDir() string {
	// Check if SKILLS_DIR environment variable is set
	if dir := os.Getenv("WEKNORA_SKILLS_DIR"); dir != "" {
		return dir
	}

	// Try to find the skills directory relative to the executable
	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		skillsDir := filepath.Join(execDir, DefaultPreloadedSkillsDir)
		if _, err := os.Stat(skillsDir); err == nil {
			return skillsDir
		}
	}

	// Try current working directory
	cwd, err := os.Getwd()
	if err == nil {
		skillsDir := filepath.Join(cwd, DefaultPreloadedSkillsDir)
		if _, err := os.Stat(skillsDir); err == nil {
			return skillsDir
		}
	}

	// Default to relative path (will be created if needed)
	return DefaultPreloadedSkillsDir
}

// ensureInitialized initializes the loader if not already done
func (s *skillService) ensureInitialized(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized {
		return nil
	}

	// Check if preloaded directory exists
	if _, err := os.Stat(s.preloadedDir); os.IsNotExist(err) {
		logger.Warnf(ctx, "Preloaded skills directory does not exist: %s", s.preloadedDir)
		// Create the directory to avoid repeated warnings
		if err := os.MkdirAll(s.preloadedDir, 0755); err != nil {
			logger.Warnf(ctx, "Failed to create preloaded skills directory: %v", err)
		}
	}

	// Create loader with preloaded directory
	s.loader = skills.NewLoader([]string{s.preloadedDir})
	s.initialized = true

	logger.Infof(ctx, "Skill service initialized with preloaded directory: %s", s.preloadedDir)

	return nil
}

// ListPreloadedSkills returns metadata for all preloaded skills
func (s *skillService) ListPreloadedSkills(ctx context.Context) ([]*skills.SkillMetadata, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	metadata, err := s.loader.DiscoverSkills()
	if err != nil {
		logger.Errorf(ctx, "Failed to discover preloaded skills: %v", err)
		return nil, fmt.Errorf("failed to discover skills: %w", err)
	}

	logger.Infof(ctx, "Discovered %d preloaded skills", len(metadata))

	return metadata, nil
}

// GetSkillByName retrieves a skill by its name
func (s *skillService) GetSkillByName(ctx context.Context, name string) (*skills.Skill, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	skill, err := s.loader.LoadSkillInstructions(name)
	if err != nil {
		logger.Errorf(ctx, "Failed to load skill %s: %v", name, err)
		return nil, fmt.Errorf("failed to load skill: %w", err)
	}

	return skill, nil
}

// GetPreloadedDir returns the configured preloaded skills directory
func (s *skillService) GetPreloadedDir() string {
	return s.preloadedDir
}

// skillNamePattern mirrors skills.Loader's naming rule (lowercase, digits, hyphens).
var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]*$`)

// resolveAgentGatewayURL returns the agent-gateway base URL used for skill
// installation. Derived from WEKNORA_AGENT_CALLBACK_URL (which points to
// {gateway}/tasks) by stripping the trailing /tasks; falls back to
// WEKNORA_AGENT_GATEWAY_URL if set directly.
func resolveAgentGatewayURL() string {
	if u := os.Getenv("WEKNORA_AGENT_GATEWAY_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	if u := os.Getenv("WEKNORA_AGENT_CALLBACK_URL"); u != "" {
		return strings.TrimRight(u, "/tasks")
	}
	return ""
}

// pushSkillToGateway sends the ZIP to the agent-gateway POST /skills/install so
// the gateway's SKILLS_DIR also gets the skill (WeKnora is the repository,
// agent-gateway is the executor). Best-effort: failure is logged, not fatal —
// the skill is already installed in WeKnora's own directory.
func (s *skillService) pushSkillToGateway(ctx context.Context, zipData []byte, skillName string) error {
	gwURL := resolveAgentGatewayURL()
	if gwURL == "" {
		logger.Warnf(ctx, "[skill-upload] WEKNORA_AGENT_CALLBACK_URL/WEKNORA_AGENT_GATEWAY_URL 未配置，跳过 gateway 安装 skill=%s", skillName)
		return nil
	}
	var body bytes.Buffer
	mw := newZipFileWriter(&body, zipData, skillName)
	if err := mw.build(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gwURL+"/skills/install", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.contentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		logger.Warnf(ctx, "[skill-upload] gateway install returned %d: %s", resp.StatusCode, string(b))
		return fmt.Errorf("gateway install status %d", resp.StatusCode)
	}
	logger.Infof(ctx, "[skill-upload] gateway skill installed: %s", skillName)
	return nil
}

// UploadSkill installs a skill from a ZIP archive.
//  1. Extracts the ZIP into <preloadedDir>/<skillName>/ (path-traversal safe).
//  2. Reloads the loader so it's immediately discoverable.
//  3. Pushes the same ZIP to the agent-gateway /skills/install (best-effort).
func (s *skillService) UploadSkill(ctx context.Context, zipData []byte, forceName string) (*skills.SkillMetadata, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize skill service: %w", err)
	}

	// --- parse zip to determine skill name & validate structure ---
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("不是有效的 ZIP: %w", err)
	}
	var skillName string
	skillFound := false
	for _, f := range zr.File {
		clean := filepath.ToSlash(filepath.Clean(f.Name))
		if clean == "SKILL.md" || strings.HasSuffix(clean, "/SKILL.md") {
			skillFound = true
			parts := strings.Split(clean, "/")
			if len(parts) >= 2 {
				skillName = parts[len(parts)-2]
			} else {
				skillName = ""
			}
			break
		}
	}
	if !skillFound {
		return nil, fmt.Errorf("ZIP 内缺少 SKILL.md")
	}
	if forceName != "" {
		skillName = forceName
	}
	skillName = strings.TrimRight(skillName, "/")
	if skillName == "" || !skillNamePattern.MatchString(skillName) {
		return nil, fmt.Errorf("非法技能名: %q（只能含小写字母/数字/-/_）", skillName)
	}

	// --- extract into preloadedDir/<skillName>/ ---
	destDir := filepath.Join(s.preloadedDir, skillName)
	base := destDir
	if err := os.RemoveAll(destDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(f.Name))
		// strip leading skillName dir if present
		rel := clean
		if strings.HasPrefix(rel, skillName+"/") {
			rel = strings.TrimPrefix(rel, skillName+"/")
		}
		if rel == "" || strings.HasPrefix(rel, "__MACOSX") {
			continue
		}
		// path traversal guard
		target := filepath.Join(base, rel)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), filepath.Clean(base)+string(os.PathSeparator)) && filepath.Clean(target) != filepath.Clean(base) {
			logger.Warnf(ctx, "[skill-upload] 拒绝越界路径 %q（skill=%s）", f.Name, skillName)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		out, err := os.Create(target)
		if err != nil {
			rc.Close()
			return nil, err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return nil, err
		}
		out.Close()
		rc.Close()
	}

	// --- reload loader so it's discoverable now ---
	s.mu.Lock()
	if s.loader != nil {
		if _, err := s.loader.Reload(); err != nil {
			logger.Warnf(ctx, "[skill-upload] loader reload warn: %v", err)
		}
	}
	s.mu.Unlock()

	// --- best-effort push to agent-gateway ---
	if err := s.pushSkillToGateway(ctx, zipData, skillName); err != nil {
		logger.Warnf(ctx, "[skill-upload] push to gateway failed (WeKnora 侧已装): %v", err)
	}

	logger.Infof(ctx, "[skill-upload] skill installed: %s -> %s", skillName, destDir)
	return &skills.SkillMetadata{Name: skillName, BasePath: destDir}, nil
}

// zipFileWriter builds a multipart/form-data body for the gateway upload.
type zipFileWriter struct {
	buf  *bytes.Buffer
	data []byte
	name string
	ct   string
}

func newZipFileWriter(buf *bytes.Buffer, data []byte, name string) *zipFileWriter {
	fw := &zipFileWriter{buf: buf, data: data, name: name}
	fw.ct = fmt.Sprintf("multipart/form-data; boundary=%s", fw.boundary())
	return fw
}

func (w *zipFileWriter) boundary() string {
	return "----WeKnoraSkillBoundary20260912"
}

func (w *zipFileWriter) build() error {
	b := w.boundary()
	var sb strings.Builder
	sb.WriteString("--" + b + "\r\n")
	sb.WriteString("Content-Disposition: form-data; name=\"file\"; filename=\"" + w.name + ".zip\"\r\n")
	sb.WriteString("Content-Type: application/zip\r\n\r\n")
	w.buf.WriteString(sb.String())
	w.buf.Write(w.data)
	w.buf.WriteString("\r\n--" + b + "\r\n")
	sb2 := "Content-Disposition: form-data; name=\"name\"\r\n\r\n" + w.name + "\r\n--" + b + "--\r\n"
	w.buf.WriteString(sb2)
	return nil
}

func (w *zipFileWriter) contentType() string { return w.ct }
