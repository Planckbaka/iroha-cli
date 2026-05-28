package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

)

// HookManager loads hook definitions from external config files and executes them.
type HookManager struct {
	mu      sync.RWMutex
	hooks   map[string][]HookDef
	timeout time.Duration
	sources []string
}

// GlobalHookManager is the singleton used by the runner.
var GlobalHookManager = NewHookManager()

// NewHookManager creates a HookManager and loads config from disk.
func NewHookManager() *HookManager {
	hm := &HookManager{
		hooks:   make(map[string][]HookDef),
		timeout: 5 * time.Second,
	}
	hm.load()
	return hm
}

// Reload discards all hooks and re-reads config files from disk.
func (hm *HookManager) Reload() {
	hm.mu.Lock()
	hm.hooks = make(map[string][]HookDef)
	hm.sources = nil
	hm.mu.Unlock()
	hm.load()
}

func (hm *HookManager) load() {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Layer 1: global config
	if home, err := os.UserHomeDir(); err == nil {
		globalIrohaPath := filepath.Join(home, ".iroha", "hooks.json")
		globalGoClaudePath := filepath.Join(home, ".go-claude", "hooks.json")
		if _, err := os.Stat(globalIrohaPath); os.IsNotExist(err) {
			if _, oldErr := os.Stat(globalGoClaudePath); oldErr == nil {
				if err := os.MkdirAll(filepath.Dir(globalIrohaPath), 0755); err != nil {
					LogError(CatSystem, "hooks_migration", "Failed to create global hooks directory during migration", err, map[string]any{"path": globalIrohaPath})
				} else if data, copyErr := os.ReadFile(globalGoClaudePath); copyErr == nil {
					if err := os.WriteFile(globalIrohaPath, data, 0644); err != nil {
						LogError(CatSystem, "hooks_migration", "Failed to migrate global hooks config", err, map[string]any{"from": globalGoClaudePath, "to": globalIrohaPath})
					}
					if err := os.Rename(globalGoClaudePath, globalGoClaudePath+".bak"); err != nil {
						LogError(CatSystem, "hooks_migration", "Failed to rename old global hooks config", err, map[string]any{"path": globalGoClaudePath})
					}
				}
			}
		}
		hm.loadFileLocked(globalIrohaPath)
	}

	// Layer 2: project config
	if cwd, err := os.Getwd(); err == nil {
		projectIrohaPath := filepath.Join(cwd, ".iroha", "hooks.json")
		projectGoClaudePath := filepath.Join(cwd, ".go-claude", "hooks.json")
		if _, err := os.Stat(projectIrohaPath); os.IsNotExist(err) {
			if _, oldErr := os.Stat(projectGoClaudePath); oldErr == nil {
				if err := os.MkdirAll(filepath.Dir(projectIrohaPath), 0755); err != nil {
					LogError(CatSystem, "hooks_migration", "Failed to create project hooks directory during migration", err, map[string]any{"path": projectIrohaPath})
				} else if data, copyErr := os.ReadFile(projectGoClaudePath); copyErr == nil {
					if err := os.WriteFile(projectIrohaPath, data, 0644); err != nil {
						LogError(CatSystem, "hooks_migration", "Failed to migrate project hooks config", err, map[string]any{"from": projectGoClaudePath, "to": projectIrohaPath})
					}
					if err := os.Rename(projectGoClaudePath, projectGoClaudePath+".bak"); err != nil {
						LogError(CatSystem, "hooks_migration", "Failed to rename old project hooks config", err, map[string]any{"path": projectGoClaudePath})
					}
				}
			}
		}
		hm.loadFileLocked(projectIrohaPath)
	}
}

func (hm *HookManager) loadFileLocked(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg HookConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		LogError(CatSession, "hook_parse_failed", fmt.Sprintf("Failed to parse hooks config file: %s", path), err, map[string]any{"path": path})
		return
	}
	if cfg.Timeout > 0 {
		hm.timeout = time.Duration(cfg.Timeout) * time.Second
	}
	loadedCount := 0
	for event, defs := range cfg.Hooks {
		hm.hooks[event] = append(hm.hooks[event], defs...)
		loadedCount += len(defs)
	}
	hm.sources = append(hm.sources, path)
	LogInfo(CatSession, "hook_load_success", fmt.Sprintf("Successfully loaded %d hooks from %s", loadedCount, path), map[string]any{
		"path":         path,
		"loaded_count": loadedCount,
	})
}

// GetSources returns the config paths that were successfully loaded.
func (hm *HookManager) GetSources() []string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	out := make([]string, len(hm.sources))
	copy(out, hm.sources)
	return out
}

// GetHooks returns a deep copy of all registered hook definitions.
func (hm *HookManager) GetHooks() map[string][]HookDef {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	snap := make(map[string][]HookDef, len(hm.hooks))
	for k, v := range hm.hooks {
		snap[k] = append([]HookDef{}, v...)
	}
	return snap
}

// IsEmpty returns true if no hooks are registered for any event.
func (hm *HookManager) IsEmpty() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	for _, defs := range hm.hooks {
		if len(defs) > 0 {
			return false
		}
	}
	return true
}

// RunHooks executes all hooks registered for the given event and returns the aggregated result.
