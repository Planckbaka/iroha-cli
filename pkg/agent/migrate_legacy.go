package agent

import (
	"os"
	"path/filepath"
)

// migrateGoClaudeIfNeeded performs a one-time migration of memory files from
// the legacy .go-claude directory to the current .iroha directory. Returns true
// if migration was attempted (i.e., the sentinel was absent).
//
// After a successful run the sentinel file ~/.iroha/.migrated is written so
// that subsequent startups skip the migration entirely.
func migrateGoClaudeIfNeeded() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	sentinel := filepath.Join(home, ".iroha", ".migrated")
	if _, err := os.Stat(sentinel); err == nil {
		return false // already migrated
	}

	migrated := false

	// Global migration: ~/.go-claude/memory -> ~/.iroha/memory
	globalIrohaDir := filepath.Join(home, ".iroha", "memory")
	globalGoClaudeDir := filepath.Join(home, ".go-claude", "memory")
	if _, err := os.Stat(globalIrohaDir); os.IsNotExist(err) {
		if _, oldErr := os.Stat(globalGoClaudeDir); oldErr == nil {
			if err := os.MkdirAll(globalIrohaDir, 0755); err != nil {
				LogError(CatSystem, "memory_migration", "Failed to create global memory directory during migration", err, map[string]any{"path": globalIrohaDir})
			} else if files, readErr := os.ReadDir(globalGoClaudeDir); readErr == nil {
				for _, f := range files {
					oldFile := filepath.Join(globalGoClaudeDir, f.Name())
					newFile := filepath.Join(globalIrohaDir, f.Name())
					if data, copyErr := os.ReadFile(oldFile); copyErr == nil {
						if err := os.WriteFile(newFile, data, 0600); err != nil {
							LogError(CatSystem, "memory_migration", "Failed to migrate memory file", err, map[string]any{"from": oldFile, "to": newFile})
						}
					}
				}
				if err := os.Rename(globalGoClaudeDir, globalGoClaudeDir+".bak"); err != nil {
					LogError(CatSystem, "memory_migration", "Failed to rename old memory directory", err, map[string]any{"path": globalGoClaudeDir})
				}
			}
			migrated = true
		}
	}

	// Project-level migration: ./.go-claude/memory -> ./.iroha/memory
	if cwd, err := os.Getwd(); err == nil {
		projectIrohaDir := filepath.Join(cwd, ".iroha", "memory")
		projectGoClaudeDir := filepath.Join(cwd, ".go-claude", "memory")
		if _, err := os.Stat(projectIrohaDir); os.IsNotExist(err) {
			if _, oldErr := os.Stat(projectGoClaudeDir); oldErr == nil {
				if err := os.MkdirAll(projectIrohaDir, 0755); err != nil {
					LogError(CatSystem, "memory_migration", "Failed to create project memory directory during migration", err, map[string]any{"path": projectIrohaDir})
				} else if files, readErr := os.ReadDir(projectGoClaudeDir); readErr == nil {
					for _, f := range files {
						oldFile := filepath.Join(projectGoClaudeDir, f.Name())
						newFile := filepath.Join(projectIrohaDir, f.Name())
						if data, copyErr := os.ReadFile(oldFile); copyErr == nil {
							if err := os.WriteFile(newFile, data, 0600); err != nil {
								LogError(CatSystem, "memory_migration", "Failed to migrate project memory file", err, map[string]any{"from": oldFile, "to": newFile})
							}
						}
					}
					if err := os.Rename(projectGoClaudeDir, projectGoClaudeDir+".bak"); err != nil {
						LogError(CatSystem, "memory_migration", "Failed to rename old project memory directory", err, map[string]any{"path": projectGoClaudeDir})
					}
				}
				migrated = true
			}
		}
	}

	// Write sentinel to prevent re-running on next startup
	_ = os.MkdirAll(filepath.Dir(sentinel), 0755)
	_ = os.WriteFile(sentinel, []byte("migrated\n"), 0600)

	return migrated
}
