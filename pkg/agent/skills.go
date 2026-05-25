package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SkillType defines how a skill is activated.
type SkillType string

const (
	// SkillTypeModelInvoked is auto-injected when trigger keywords match user prompt.
	SkillTypeModelInvoked SkillType = "model_invoked"
	// SkillTypeUserInvoked is available as /skill <name> slash command.
	SkillTypeUserInvoked SkillType = "user_invoked"
	// SkillTypeAlways is always injected into system prompt.
	SkillTypeAlways SkillType = "always"
)

// SkillManifest defines the structure for a skill.json manifest file.
type SkillManifest struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	Triggers         []string  `json:"triggers"`
	Tags             []string  `json:"tags,omitempty"`
	InstructionsFile string    `json:"instructions_file"`
	Type             SkillType `json:"type"`
	// BaseDir is set during discovery to resolve InstructionsFile paths.
	BaseDir string `json:"-"`
}

// SkillManager discovers, loads, and matches skills.
type SkillManager struct {
	mu     sync.RWMutex
	skills []*SkillManifest
	byID   map[string]*SkillManifest
}

// GlobalSkillManager is the singleton skill manager.
var GlobalSkillManager = &SkillManager{
	byID: make(map[string]*SkillManifest),
}

// LoadSkills discovers and parses skill manifests from global + project paths.
func (sm *SkillManager) LoadSkills() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.skills = nil
	sm.byID = make(map[string]*SkillManifest)

	var allSkills []*SkillManifest

	// Global skills: ~/.iroha/skills/<name>/skill.json
	if home, err := os.UserHomeDir(); err == nil {
		globalDir := filepath.Join(home, ".iroha", "skills")
		if skills, err := discoverSkillsInDir(globalDir); err == nil {
			allSkills = append(allSkills, skills...)
		}
	}

	// Project skills: .iroha/skills/<name>/skill.json (overrides global by ID)
	if wd, err := os.Getwd(); err == nil {
		root := findProjectRoot(wd)
		projectDir := filepath.Join(root, ".iroha", "skills")
		if skills, err := discoverSkillsInDir(projectDir); err == nil {
			allSkills = append(allSkills, skills...)
		}
	}

	// Build index, project skills override global with same ID
	for _, s := range allSkills {
		sm.byID[s.ID] = s
	}

	// Rebuild slice from unique byID values
	sm.skills = make([]*SkillManifest, 0, len(sm.byID))
	for _, s := range sm.byID {
		sm.skills = append(sm.skills, s)
	}

	LogInfo(CatSystem, "skills_loaded", fmt.Sprintf("Loaded %d skill manifests", len(sm.skills)), map[string]any{
		"count": len(sm.skills),
	})
	return nil
}

// discoverSkillsInDir scans a directory for skill subdirectories containing skill.json.
func discoverSkillsInDir(dir string) ([]*SkillManifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var skills []*SkillManifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(dir, entry.Name(), "skill.json")
		s, err := loadSkillManifest(manifestPath)
		if err != nil {
			LogWarn(CatSystem, "skill_discovery_skip", fmt.Sprintf("Skipping skill: %v", err), map[string]any{
				"path": manifestPath,
			})
			continue
		}
		s.BaseDir = filepath.Join(dir, entry.Name())
		skills = append(skills, s)
		LogInfo(CatSystem, "skill_discovered", fmt.Sprintf("Discovered skill: %s (%s)", s.Name, s.ID), map[string]any{
			"id":   s.ID,
			"name": s.Name,
			"type": string(s.Type),
		})
	}
	return skills, nil
}

// loadSkillManifest reads and validates a skill.json file.
func loadSkillManifest(path string) (*SkillManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill manifest %s: %w", path, err)
	}

	var manifest SkillManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse skill manifest %s: %w", path, err)
	}

	if strings.TrimSpace(manifest.ID) == "" {
		return nil, fmt.Errorf("skill manifest missing required field: id")
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return nil, fmt.Errorf("skill manifest missing required field: name")
	}
	if manifest.Type == "" {
		manifest.Type = SkillTypeModelInvoked
	}
	if manifest.InstructionsFile == "" {
		manifest.InstructionsFile = "SKILL.md"
	}

	return &manifest, nil
}

// MatchTriggers returns all skills whose trigger keywords match the prompt (case-insensitive).
func (sm *SkillManager) MatchTriggers(prompt string) []*SkillManifest {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	lower := strings.ToLower(prompt)
	var matched []*SkillManifest

	for _, s := range sm.skills {
		if s.Type != SkillTypeModelInvoked {
			continue
		}
		for _, trigger := range s.Triggers {
			if strings.Contains(lower, strings.ToLower(trigger)) {
				matched = append(matched, s)
				break
			}
		}
	}
	return matched
}

// GetSkillByID returns a skill manifest by its ID (for /skill command).
func (sm *SkillManager) GetSkillByID(id string) *SkillManifest {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.byID[id]
}

// GetAlwaysSkills returns all skills with type "always".
func (sm *SkillManager) GetAlwaysSkills() []*SkillManifest {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var always []*SkillManifest
	for _, s := range sm.skills {
		if s.Type == SkillTypeAlways {
			always = append(always, s)
		}
	}
	return always
}

// GetUserInvokedSkills returns all skills with type "user_invoked".
func (sm *SkillManager) GetUserInvokedSkills() []*SkillManifest {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var userInvoked []*SkillManifest
	for _, s := range sm.skills {
		if s.Type == SkillTypeUserInvoked {
			userInvoked = append(userInvoked, s)
		}
	}
	return userInvoked
}

// LoadInstructions reads the skill's instructions file (SKILL.md) from disk.
func LoadInstructions(skill *SkillManifest) (string, error) {
	if skill.BaseDir == "" {
		return "", fmt.Errorf("skill %s has no base directory", skill.ID)
	}
	path := filepath.Join(skill.BaseDir, skill.InstructionsFile)

	// Canonicalize and verify path stays within BaseDir
	absBase, err := filepath.Abs(skill.BaseDir)
	if err != nil {
		return "", fmt.Errorf("skill %s: invalid base directory: %w", skill.ID, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("skill %s: invalid instructions path: %w", skill.ID, err)
	}
	if !strings.HasPrefix(absPath, absBase+string(os.PathSeparator)) && absPath != absBase {
		return "", fmt.Errorf("skill %s: instructions path escapes base directory", skill.ID)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to read skill instructions %s: %w", path, err)
	}
	return string(data), nil
}

// AllSkills returns a copy of all loaded skill manifests.
func (sm *SkillManager) AllSkills() []*SkillManifest {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	out := make([]*SkillManifest, len(sm.skills))
	copy(out, sm.skills)
	return out
}
