package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/agent/skills"
)

// SkillService defines the interface for skill business logic
type SkillService interface {
	// ListPreloadedSkills returns metadata for all preloaded skills
	ListPreloadedSkills(ctx context.Context) ([]*skills.SkillMetadata, error)

	// GetSkillByName retrieves a skill by its name
	GetSkillByName(ctx context.Context, name string) (*skills.Skill, error)

	// UploadSkill installs a skill from a ZIP archive: extracts into the skills
	// directory, reloads the loader, and pushes the same archive to the
	// agent-gateway POST /skills/install so the gateway's SKILLS_DIR also gets
	// the skill (WeKnora = skill repository, agent-gateway = skill executor).
	// Returns the installed skill's metadata.
	UploadSkill(ctx context.Context, zipData []byte, forceName string) (*skills.SkillMetadata, error)

	// DeleteSkill removes a skill from the preloaded directory, reloads the
	// loader, and notifies the agent-gateway DELETE /skills/{name} so the
	// gateway's install dir also drops it. Returns the deleted skill's name.
	DeleteSkill(ctx context.Context, name string) error

	// GetSkillDetail returns a skill's file listing (relative paths + sizes)
	// for the management UI's detail view.
	GetSkillDetail(ctx context.Context, name string) (*SkillDetail, error)
	// ExportSkill packs a skill directory into a ZIP archive whose layout
	// (<skillDir>/...) is exactly what UploadSkill accepts, so an exported
	// ZIP can be re-imported as-is.
	ExportSkill(ctx context.Context, name string) ([]byte, error)
}

// SkillDetail is the detail payload for a skill management view.
type SkillDetail struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Path        string      `json:"path"`
	FileCount   int         `json:"file_count"`
	Files       []SkillFile `json:"files"`
	TotalFiles  int         `json:"total_files"`
}

// SkillFile is one file entry inside a skill directory.
type SkillFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
