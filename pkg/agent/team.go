package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// NewTeamManager creates a new TeamManager.
func NewTeamManager() *TeamManager {
	return &TeamManager{
		teamDir:     ResolveTeamDir(),
		teammates:   make(map[string]*Teammate),
		activeLoops: make(map[string]chan struct{}),
		watchdogs:   make(map[string]*Watchdog),
		cancelFuncs: make(map[string]context.CancelFunc),
	}
}

// GlobalTeamManager is the singleton team manager.
var GlobalTeamManager = NewTeamManager()

// ResolveTeamDir locates the persistent directory for teams.
func ResolveTeamDir() string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findProjectRoot(wd)
	teamDir := filepath.Join(root, ".team")
	_ = os.MkdirAll(teamDir, 0755)
	_ = os.MkdirAll(filepath.Join(teamDir, "inbox"), 0755)
	return teamDir
}

// LoadConfig reads the roster from disk.
func (tm *TeamManager) LoadConfig() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	configPath := filepath.Join(tm.teamDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			tm.teammates = make(map[string]*Teammate)
		} else {
			return fmt.Errorf("failed to read team config: %w", err)
		}
	} else {
		var cfg TeamConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("failed to parse team config: %w", err)
		}

		tm.teammates = make(map[string]*Teammate)
		for i := range cfg.Teammates {
			t := cfg.Teammates[i]
			tm.teammates[t.Name] = &t
		}
	}

	_ = tm.loadYAMLAgents()
	return nil
}

// loadYAMLAgents dynamically scans and registers YAML-declared subagents from (.iroha/agents/ & .claude/agents/)
func (tm *TeamManager) loadYAMLAgents() error {
	var searchDirs []string

	// Local project directories
	if cwd, err := os.Getwd(); err == nil {
		searchDirs = append(searchDirs,
			filepath.Join(cwd, ".iroha", "agents"),
			filepath.Join(cwd, ".claude", "agents"),
		)
	}

	// User home directories
	if home, err := os.UserHomeDir(); err == nil {
		searchDirs = append(searchDirs,
			filepath.Join(home, ".iroha", "agents"),
			filepath.Join(home, ".claude", "agents"),
		)
	}

	type yamlMeta struct {
		Name        string `yaml:"name"`
		Role        string `yaml:"role,omitempty"`
		Description string `yaml:"description,omitempty"`
		Type        string `yaml:"type,omitempty"`
	}

	for _, dir := range searchDirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue // skip directory if doesn't exist
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(file.Name()))
			if ext != ".yaml" && ext != ".yml" {
				continue
			}

			filePath := filepath.Join(dir, file.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			// Parse frontmatter
			content := string(data)
			var frontmatter string
			var systemPrompt string

			if strings.HasPrefix(content, "---") {
				// Format:
				// ---
				// yaml metadata
				// ---
				// system prompt
				parts := strings.SplitN(content, "---", 3)
				if len(parts) >= 3 {
					frontmatter = parts[1]
					systemPrompt = strings.TrimSpace(parts[2])
				} else {
					frontmatter = content
				}
			} else {
				frontmatter = content
			}

			var meta yamlMeta
			if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
				continue // bad YAML metadata, skip
			}

			if meta.Name == "" {
				continue
			}

			role := meta.Role
			if role == "" {
				role = meta.Description
			}
			if role == "" {
				role = "Specialist Agent"
			}

			tm.teammates[meta.Name] = &Teammate{
				Name:         meta.Name,
				Role:         role,
				Type:         meta.Type,
				SystemPrompt: systemPrompt,
				Status:       "idle",
				LastActive:   time.Now(),
			}
		}
	}
	return nil
}

// SaveConfig writes the roster to disk.
func (tm *TeamManager) SaveConfig() error {
	configPath := filepath.Join(tm.teamDir, "config.json")
	var cfg TeamConfig
	cfg.Teammates = make([]Teammate, 0, len(tm.teammates))
	for _, t := range tm.teammates {
		cfg.Teammates = append(cfg.Teammates, *t)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal team config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write team config: %w", err)
	}
	return nil
}

// RegisterTeammate creates or updates a specialist and saves it.
func (tm *TeamManager) RegisterTeammate(name, role, systemPrompt, agentType string) (*Teammate, error) {
	if name == "" || role == "" {
		return nil, fmt.Errorf("name and role are required")
	}

	if err := tm.LoadConfig(); err != nil {
		return nil, err
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	t, ok := tm.teammates[name]
	if !ok {
		t = &Teammate{Name: name}
		tm.teammates[name] = t
	}
	t.Role = role
	t.Type = agentType
	t.SystemPrompt = systemPrompt
	t.Status = "idle"
	t.LastActive = time.Now()

	if err := tm.SaveConfig(); err != nil {
		LogError(CatSubagent, "subagent_register_failed", fmt.Sprintf("Failed to save team config during teammate '%s' registration", name), err, map[string]any{"name": name})
		return nil, err
	}

	LogInfo(CatSubagent, "subagent_registered", fmt.Sprintf("Specialist teammate '%s' registered (Role: %s)", name, role), map[string]any{
		"name": name,
		"role": role,
	})

	return t, nil
}

// GetTeammate retrieves a teammate by name.
func (tm *TeamManager) GetTeammate(name string) (*Teammate, error) {
	if err := tm.LoadConfig(); err != nil {
		return nil, err
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	t, ok := tm.teammates[name]
	if !ok {
		return nil, fmt.Errorf("teammate '%s' not found", name)
	}
	return t, nil
}

// ListTeammates lists all registered teammates.
func (tm *TeamManager) ListTeammates() ([]Teammate, error) {
	if err := tm.LoadConfig(); err != nil {
		return nil, err
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	list := make([]Teammate, 0, len(tm.teammates))
	for _, t := range tm.teammates {
		list = append(list, *t)
	}
	return list, nil
}

// StartTeammateLoop spawns a background goroutine for a teammate to process its inbox.
// When process isolation is enabled, it spawns a child process instead.
