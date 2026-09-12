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
}
