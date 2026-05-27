package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryManager is a file-based persistent store for cross-session durable facts.
//
// Storage layout (two layers, project takes priority):
//
//	~/.iroha/memory/        global memories
//	  MEMORY.md                 index file (auto-generated)
//	  prefer_pnpm.md            one .md per entry
//
//	./.iroha/memory/        project-level memories (merged on top)
//	  MEMORY.md
//	  no_snapshot_edits.md
//
// Each memory file uses YAML frontmatter:
//
//	---
//	name: prefer_pnpm
//	description: User prefers pnpm over npm
//	type: user
//	updated_at: 2026-05-20T08:00:00Z
//	---
//	The user explicitly prefers pnpm for all package management commands.
type MemoryManager struct {
	mu      sync.RWMutex
	entries map[string]*MemoryEntry // keyed by Name
	dirs    []string                // loaded directory paths (for display)
}

// GlobalDreamConsolidator is the singleton consolidator for persistent memories.
var GlobalDreamConsolidator = NewDreamConsolidator()

// GlobalMemoryManager is the singleton used throughout the session.
var GlobalMemoryManager = NewMemoryManager()

// NewMemoryManager creates a MemoryManager and loads existing memories from disk.
func NewMemoryManager() *MemoryManager {
	mm := &MemoryManager{
		entries: make(map[string]*MemoryEntry),
	}
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.loadLocked()

	if GlobalDreamConsolidator != nil {
		GlobalDreamConsolidator.IncrementSession()
	}
	return mm
}

// Reload discards all in-memory entries and re-reads from disk.
func (mm *MemoryManager) Reload() {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.entries = make(map[string]*MemoryEntry)
	mm.dirs = nil
	mm.loadLocked()
}

func (mm *MemoryManager) loadLocked() {
	// One-time legacy migration gated by ~/.iroha/.migrated sentinel
	migrateGoClaudeIfNeeded()

	// Layer 1: global memories
	if home, err := os.UserHomeDir(); err == nil {
		globalIrohaDir := filepath.Join(home, ".iroha", "memory")
		mm.loadDirLocked(globalIrohaDir)
	}
	// Layer 2: project memories (merged on top; same name overwrites global)
	if cwd, err := os.Getwd(); err == nil {
		projectIrohaDir := filepath.Join(cwd, ".iroha", "memory")
		mm.loadDirLocked(projectIrohaDir)
	}

	// Layer 3: Bidirectional sync back from AGENTS.md (takes absolute priority)
	_ = mm.syncFromAgentsMDLocked()
}

func (mm *MemoryManager) loadDirLocked(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			LogError(CatSession, "memory_load_dir_failed", fmt.Sprintf("Failed to read memory directory: %s", dir), err, map[string]any{"path": dir})
		}
		return // directory absent — silently skip
	}
	loaded := 0
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") || de.Name() == "MEMORY.md" {
			continue
		}
		filePath := filepath.Join(dir, de.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			LogError(CatSession, "memory_file_read_failed", fmt.Sprintf("Failed to read memory file: %s", filePath), err, map[string]any{"path": filePath})
			continue
		}
		entry, err := parseFrontmatter(string(data))
		if err != nil || entry == nil {
			LogError(CatSession, "memory_parse_failed", fmt.Sprintf("Failed to parse frontmatter from memory: %s", filePath), err, map[string]any{"path": filePath})
			continue
		}
		entry.File = de.Name()
		mm.entries[entry.Name] = entry
		loaded++
	}
	if loaded > 0 {
		mm.dirs = append(mm.dirs, dir)
		LogInfo(CatSession, "memory_load_success", fmt.Sprintf("Loaded %d memories from directory: %s", loaded, dir), map[string]any{
			"directory":    dir,
			"loaded_count": loaded,
		})
	}
}

// Save writes a memory entry to disk and updates the in-memory store.
// If an entry with the same name already exists it is overwritten.
func (mm *MemoryManager) Save(name, description string, memType MemoryType, content string) error {
	if !validMemoryTypes[memType] {
		err := fmt.Errorf("invalid memory type %q: must be one of user, feedback, project, reference", memType)
		LogError(CatSession, "memory_save_invalid_type", "Attempted to save memory with invalid type", err, map[string]any{"type": memType, "name": name})
		return err
	}
	if strings.TrimSpace(name) == "" {
		err := fmt.Errorf("memory name cannot be empty")
		LogError(CatSession, "memory_save_empty_name", "Attempted to save memory with empty name", err, nil)
		return err
	}

	// Check entry cap for new entries (updates don't count)
	mm.mu.RLock()
	_, exists := mm.entries[name]
	currentCount := len(mm.entries)
	mm.mu.RUnlock()
	if !exists && currentCount >= MaxMemoryEntries {
		return fmt.Errorf("memory store full: max %d entries reached", MaxMemoryEntries)
	}

	// Determine save directory: prefer project-level
	saveDir, err := projectMemoryDir()
	if err != nil {
		errWrap := fmt.Errorf("cannot determine memory directory: %w", err)
		LogError(CatSession, "memory_save_dir_failed", "Failed to resolve project memory directory", errWrap, nil)
		return errWrap
	}
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		errWrap := fmt.Errorf("cannot create memory directory: %w", err)
		LogError(CatSession, "memory_create_dir_failed", fmt.Sprintf("Failed to create memory directory: %s", saveDir), errWrap, map[string]any{"path": saveDir})
		return errWrap
	}

	filename := slugify(name) + ".md"
	now := time.Now().UTC()

	entry := &MemoryEntry{
		Name:        name,
		Description: description,
		Type:        memType,
		Content:     content,
		UpdatedAt:   now,
		File:        filename,
	}

	filePath := filepath.Join(saveDir, filename)
	text := renderFrontmatter(entry)
	if err := os.WriteFile(filePath, []byte(text), 0600); err != nil {
		errWrap := fmt.Errorf("cannot write memory file: %w", err)
		LogError(CatSession, "memory_write_failed", fmt.Sprintf("Failed to write memory file: %s", filePath), errWrap, map[string]any{"path": filePath, "name": name})
		return errWrap
	}

	mm.mu.Lock()
	mm.entries[name] = entry
	if len(mm.dirs) == 0 || mm.dirs[len(mm.dirs)-1] != saveDir {
		mm.dirs = append(mm.dirs, saveDir)
	}
	mm.mu.Unlock()

	mm.rebuildIndex(saveDir)
	_ = syncToAgentsMD(entry, false)

	LogAudit(CatSession, "memory_save", fmt.Sprintf("Memory entry '%s' (%s) saved successfully", name, memType), map[string]any{
		"name":        name,
		"type":        memType,
		"description": description,
		"path":        filePath,
	})

	return nil
}

// List returns a snapshot of all memory entries, grouped by type.
func (mm *MemoryManager) List() map[MemoryType][]*MemoryEntry {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	out := make(map[MemoryType][]*MemoryEntry)
	for _, e := range mm.entries {
		out[e.Type] = append(out[e.Type], e)
	}
	return out
}

// Count returns the total number of stored entries.
func (mm *MemoryManager) Count() int {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return len(mm.entries)
}

// GetDirs returns the directories that were successfully loaded from.
func (mm *MemoryManager) GetDirs() []string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	out := make([]string, len(mm.dirs))
	copy(out, mm.dirs)
	return out
}

// BuildSystemPromptSection returns a Markdown block that can be appended to
// the system prompt so the LLM has access to all stored durable facts.
// Returns an empty string if there are no memories.
func (mm *MemoryManager) BuildSystemPromptSection(currentPrompt ...string) string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	if len(mm.entries) == 0 {
		return ""
	}

	promptText := ""
	if len(currentPrompt) > 0 {
		promptText = strings.ToLower(currentPrompt[0])
	}

	var sb strings.Builder
	sb.WriteString("## Persistent Memories (survive across sessions)\n\n")
	sb.WriteString("> These facts were explicitly saved by the user or agent. ")
	sb.WriteString("They are durable preferences, corrections, and project context.\n")
	sb.WriteString("> Treat them as strong hints, but current observation always takes precedence.\n\n")

	typeOrder := []MemoryType{MemTypeUser, MemTypeFeedback, MemTypeProject, MemTypeReference}
	typeLabels := map[MemoryType]string{
		MemTypeUser:      "👤 User Preferences",
		MemTypeFeedback:  "🔁 Feedback & Corrections",
		MemTypeProject:   "📁 Project Facts",
		MemTypeReference: "🔗 External References",
	}

	injectedCount := 0
	for _, t := range typeOrder {
		var typed []*MemoryEntry
		for _, e := range mm.entries {
			if e.Type != t {
				continue
			}

			// Filter logic
			shouldInject := false
			if promptText == "" || t == MemTypeFeedback {
				// Feedback corrections are always injected for safety, or if no prompt is provided
				shouldInject = true
			} else {
				// Fuzzy check keywords in Name, Description, Content
				keywords := tokenizeKeywords(e.Name + " " + e.Description)
				for _, kw := range keywords {
					if strings.Contains(promptText, kw) {
						shouldInject = true
						break
					}
				}
			}

			if shouldInject {
				typed = append(typed, e)
			}
		}
		if len(typed) == 0 {
			continue
		}
		sb.WriteString("### " + typeLabels[t] + "\n\n")
		for _, e := range typed {
			sb.WriteString(fmt.Sprintf("**%s**: %s\n", e.Name, e.Description))
			if body := strings.TrimSpace(e.Content); body != "" {
				sb.WriteString(body + "\n")
			}
			sb.WriteString("\n")
		}
		injectedCount += len(typed)
	}

	if injectedCount == 0 {
		return ""
	}
	return sb.String()
}

// tokenizeKeywords splits text into lowercase words, skipping short common stop words
func tokenizeKeywords(text string) []string {
	text = strings.ToLower(text)
	// Replace non-alphanumeric with spaces
	re := regexp.MustCompile(`[^a-z0-9]+`)
	cleaned := re.ReplaceAllString(text, " ")
	words := strings.Fields(cleaned)

	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "you": true, "with": true, "this": true, "that": true,
		"your": true, "from": true, "have": true, "will": true, "about": true, "want": true,
	}

	var res []string
	for _, w := range words {
		if len(w) >= 3 && !stopWords[w] {
			res = append(res, w)
		}
	}
	return res
}

// rebuildIndex rewrites the MEMORY.md index file in the given directory.
func (mm *MemoryManager) rebuildIndex(dir string) {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	mm.rebuildIndexLocked(dir)
}

func (mm *MemoryManager) rebuildIndexLocked(dir string) {
	var lines []string
	lines = append(lines, "# Memory Index", "")
	for _, e := range mm.entries {
		lines = append(lines, fmt.Sprintf("- %s: %s [%s]", e.Name, e.Description, e.Type))
	}
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

// Search returns memory entries matching the query using case-insensitive token matching.
// Results are sorted by relevance (number of matching tokens, descending).
func (mm *MemoryManager) Search(query string) []*MemoryEntry {
	tokens := tokenizeKeywords(query)
	if len(tokens) == 0 {
		return nil
	}

	mm.mu.RLock()
	defer mm.mu.RUnlock()

	type scored struct {
		entry *MemoryEntry
		score int
	}

	var matches []scored
	for _, e := range mm.entries {
		score := 0
		haystack := strings.ToLower(e.Name + " " + e.Description + " " + e.Content)
		for _, tok := range tokens {
			if strings.Contains(haystack, tok) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scored{entry: e, score: score})
		}
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].score > matches[j].score })

	result := make([]*MemoryEntry, len(matches))
	for i, m := range matches {
		result[i] = m.entry
	}
	return result
}

// Update modifies an existing memory entry by name. Returns error if not found.
func (mm *MemoryManager) Update(name, description string, memType MemoryType, content string) error {
	if !validMemoryTypes[memType] {
		err := fmt.Errorf("invalid memory type %q: must be one of user, feedback, project, reference", memType)
		LogError(CatSession, "memory_update_invalid_type", "Attempted to update memory with invalid type", err, map[string]any{"type": memType, "name": name})
		return err
	}

	mm.mu.Lock()
	existing, ok := mm.entries[name]
	if !ok {
		mm.mu.Unlock()
		err := fmt.Errorf("memory entry %q not found", name)
		LogError(CatSession, "memory_update_not_found", "Attempted to update non-existent memory", err, map[string]any{"name": name})
		return err
	}
	fileSnapshot := existing.File
	// Find which directory the file is in
	var saveDir string
	for _, d := range mm.dirs {
		candidate := filepath.Join(d, fileSnapshot)
		if _, err := os.Stat(candidate); err == nil {
			saveDir = d
			break
		}
	}
	mm.mu.Unlock()

	if saveDir == "" {
		// Fallback: use project memory dir.
		var err error
		saveDir, err = projectMemoryDir()
		if err != nil {
			return fmt.Errorf("cannot determine memory directory: %w", err)
		}
	}

	filename := slugify(name) + ".md"
	now := time.Now().UTC()

	entry := &MemoryEntry{
		Name:        name,
		Description: description,
		Type:        memType,
		Content:     content,
		UpdatedAt:   now,
		File:        filename,
	}

	filePath := filepath.Join(saveDir, filename)
	text := renderFrontmatter(entry)
	if err := os.WriteFile(filePath, []byte(text), 0600); err != nil {
		errWrap := fmt.Errorf("cannot write memory file: %w", err)
		LogError(CatSession, "memory_update_write_failed", fmt.Sprintf("Failed to write updated memory file: %s", filePath), errWrap, map[string]any{"path": filePath, "name": name})
		return errWrap
	}

	mm.mu.Lock()
	mm.entries[name] = entry
	mm.mu.Unlock()

	mm.rebuildIndex(saveDir)
	_ = syncToAgentsMD(entry, false)

	LogAudit(CatSession, "memory_update", fmt.Sprintf("Memory entry '%s' (%s) updated successfully", name, memType), map[string]any{
		"name":        name,
		"type":        memType,
		"description": description,
		"path":        filePath,
	})

	return nil
}

// Delete removes a memory entry by name. Returns error if not found.
func (mm *MemoryManager) Delete(name string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	return mm.deleteLocked(name, false)
}

func (mm *MemoryManager) deleteLocked(name string, skipSyncToAgents bool) error {
	existing, ok := mm.entries[name]
	if !ok {
		err := fmt.Errorf("memory entry %q not found", name)
		LogError(CatSession, "memory_delete_not_found", "Attempted to delete non-existent memory", err, map[string]any{"name": name})
		return err
	}
	delete(mm.entries, name)

	// Find the directory containing the file and delete it.
	deletedDir := ""
	for _, dir := range mm.dirs {
		filePath := filepath.Join(dir, existing.File)
		if _, err := os.Stat(filePath); err == nil {
			_ = os.Remove(filePath)
			deletedDir = dir
			break
		}
	}

	if deletedDir != "" {
		mm.rebuildIndexLocked(deletedDir)
	}
	if !skipSyncToAgents {
		_ = syncToAgentsMD(existing, true)
	}

	LogAudit(CatSession, "memory_delete", fmt.Sprintf("Memory entry '%s' deleted", name), map[string]any{
		"name": name,
		"file": existing.File,
	})

	return nil
}

// projectMemoryDir returns ./.iroha/memory (creating if needed).
func projectMemoryDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".iroha", "memory"), nil
}
