package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MemoryType classifies what kind of durable fact a memory entry represents.
// Only four types exist — if a fact doesn't fit, it probably shouldn't be stored.
type MemoryType string

const (
	// MemTypeUser stores stable user preferences across sessions.
	// Examples: prefers pnpm, wants concise answers, dislikes large refactors without a plan.
	MemTypeUser MemoryType = "user"

	// MemTypeFeedback stores corrections the user wants enforced.
	// Examples: "do not change test snapshots unless I ask".
	MemTypeFeedback MemoryType = "feedback"

	// MemTypeProject stores durable project facts not obvious from the repo.
	// Examples: "the legacy dir cannot be deleted — deployment depends on it".
	MemTypeProject MemoryType = "project"

	// MemTypeReference stores pointers to external resources.
	// Examples: incident board URL, monitoring dashboard location.
	MemTypeReference MemoryType = "reference"
)

// validMemoryTypes is the closed set of allowed types.
var validMemoryTypes = map[MemoryType]bool{
	MemTypeUser:      true,
	MemTypeFeedback:  true,
	MemTypeProject:   true,
	MemTypeReference: true,
}

// MemoryEntry is a single durable fact stored on disk.
type MemoryEntry struct {
	// Name is the unique key used to identify and overwrite the entry.
	Name string
	// Description is a one-line human-readable summary (shown in the index and prompt).
	Description string
	// Type classifies the entry for grouping and filtering.
	Type MemoryType
	// Content is the full text body of the memory (injected verbatim into the prompt).
	Content string
	// UpdatedAt records when the entry was last written.
	UpdatedAt time.Time
	// File is the filename on disk (derived from Name, not user-controlled).
	File string
}

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

// agentsMDMu protects concurrent reads/writes to AGENTS.md.
var agentsMDMu sync.Mutex

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
	// Layer 1: global memories
	if home, err := os.UserHomeDir(); err == nil {
		globalIrohaDir := filepath.Join(home, ".iroha", "memory")
		globalGoClaudeDir := filepath.Join(home, ".go-claude", "memory")
		if _, err := os.Stat(globalIrohaDir); os.IsNotExist(err) {
			if _, oldErr := os.Stat(globalGoClaudeDir); oldErr == nil {
				_ = os.MkdirAll(globalIrohaDir, 0755)
				if files, readErr := os.ReadDir(globalGoClaudeDir); readErr == nil {
					for _, f := range files {
						oldFile := filepath.Join(globalGoClaudeDir, f.Name())
						newFile := filepath.Join(globalIrohaDir, f.Name())
						if data, copyErr := os.ReadFile(oldFile); copyErr == nil {
							_ = os.WriteFile(newFile, data, 0600)
						}
					}
					_ = os.Rename(globalGoClaudeDir, globalGoClaudeDir+".bak")
				}
			}
		}
		mm.loadDirLocked(globalIrohaDir)
	}
	// Layer 2: project memories (merged on top; same name overwrites global)
	if cwd, err := os.Getwd(); err == nil {
		projectIrohaDir := filepath.Join(cwd, ".iroha", "memory")
		projectGoClaudeDir := filepath.Join(cwd, ".go-claude", "memory")
		if _, err := os.Stat(projectIrohaDir); os.IsNotExist(err) {
			if _, oldErr := os.Stat(projectGoClaudeDir); oldErr == nil {
				_ = os.MkdirAll(projectIrohaDir, 0755)
				if files, readErr := os.ReadDir(projectGoClaudeDir); readErr == nil {
					for _, f := range files {
						oldFile := filepath.Join(projectGoClaudeDir, f.Name())
						newFile := filepath.Join(projectIrohaDir, f.Name())
						if data, copyErr := os.ReadFile(oldFile); copyErr == nil {
							_ = os.WriteFile(newFile, data, 0600)
						}
					}
					_ = os.Rename(projectGoClaudeDir, projectGoClaudeDir+".bak")
				}
			}
		}
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

// ── Frontmatter helpers ────────────────────────────────────────────────────

var frontmatterRe = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n(.*)`)

func parseFrontmatter(text string) (*MemoryEntry, error) {
	m := frontmatterRe.FindStringSubmatch(text)
	if m == nil {
		return nil, fmt.Errorf("no frontmatter found")
	}
	header, body := m[1], strings.TrimSpace(m[2])

	entry := &MemoryEntry{Content: body}
	for _, line := range strings.Split(header, "\n") {
		k, v, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "name":
			entry.Name = v
		case "description":
			entry.Description = v
		case "type":
			entry.Type = MemoryType(v)
		case "updated_at":
			t, err := time.Parse(time.RFC3339, v)
			if err == nil {
				entry.UpdatedAt = t
			}
		}
	}
	if entry.Name == "" {
		return nil, fmt.Errorf("memory entry missing 'name' field")
	}
	return entry, nil
}

func renderFrontmatter(e *MemoryEntry) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\nupdated_at: %s\n---\n%s\n",
		e.Name, e.Description, e.Type, e.UpdatedAt.Format(time.RFC3339), e.Content)
}

// slugify converts a name to a safe filename-compatible string.
var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphanumRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "memory"
	}
	return s
}

// MaxMemoryEntries caps the total number of stored memory entries.
const MaxMemoryEntries = 100

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

	// Sort by relevance (descending score).
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].score > matches[i].score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}

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

type agentsBlock struct {
	name       string
	headerLine string
	bodyLines  []string
}

func syncToAgentsMD(entry *MemoryEntry, isDelete bool) error {
	agentsMDMu.Lock()
	defer agentsMDMu.Unlock()

	agentsPath := "AGENTS.md"

	// Read AGENTS.md
	data, err := os.ReadFile(agentsPath)
	var content string
	if err != nil {
		if os.IsNotExist(err) {
			content = "# Project Agents Configuration\n\n"
		} else {
			return err
		}
	} else {
		content = string(data)
	}

	const sectionTitle = "## Agent Dynamic Learnings"

	// Find or create the ## Agent Dynamic Learnings section
	var beforeSection, afterSection string
	idx := strings.Index(content, sectionTitle)
	if idx == -1 {
		if !strings.HasSuffix(content, "\n\n") {
			if strings.HasSuffix(content, "\n") {
				content += "\n"
			} else {
				content += "\n\n"
			}
		}
		content += sectionTitle + "\n\n"
		beforeSection = content
		afterSection = ""
	} else {
		beforeSection = content[:idx+len(sectionTitle)]
		afterSection = content[idx+len(sectionTitle):]
	}

	// Parse blocks from afterSection
	lines := strings.Split(afterSection, "\n")
	var newSectionLines []string
	var blocks []agentsBlock
	var currentBlock *agentsBlock

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- **") && strings.Contains(trimmed, "** (") {
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
			}
			nameEnd := strings.Index(trimmed[4:], "**")
			var name string
			if nameEnd != -1 {
				name = trimmed[4 : 4+nameEnd]
			}
			currentBlock = &agentsBlock{
				name:       name,
				headerLine: line,
			}
		} else if currentBlock != nil && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") || line == "") {
			currentBlock.bodyLines = append(currentBlock.bodyLines, line)
		} else {
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			newSectionLines = append(newSectionLines, line)
		}
	}
	if currentBlock != nil {
		blocks = append(blocks, *currentBlock)
	}

	// Update or delete blocks
	found := false
	var updatedBlocks []agentsBlock
	for _, b := range blocks {
		if b.name == entry.Name {
			found = true
			if !isDelete {
				updatedBlocks = append(updatedBlocks, makeAgentsBlock(entry))
			}
		} else {
			updatedBlocks = append(updatedBlocks, b)
		}
	}
	if !found && !isDelete {
		updatedBlocks = append(updatedBlocks, makeAgentsBlock(entry))
	}

	// Reconstruct the section
	var sb strings.Builder
	for _, b := range updatedBlocks {
		sb.WriteString(b.headerLine + "\n")
		for _, bl := range b.bodyLines {
			sb.WriteString(bl + "\n")
		}
	}

	nonEmptyFound := false
	var trailingLines []string
	for i := len(newSectionLines) - 1; i >= 0; i-- {
		l := newSectionLines[i]
		if strings.TrimSpace(l) != "" {
			nonEmptyFound = true
		}
		if nonEmptyFound {
			trailingLines = append([]string{l}, trailingLines...)
		}
	}
	for _, tl := range trailingLines {
		sb.WriteString(tl + "\n")
	}

	finalContent := beforeSection + "\n" + sb.String()
	reConsecutiveNewlines := regexp.MustCompile(`\n{3,}`)
	finalContent = reConsecutiveNewlines.ReplaceAllString(finalContent, "\n\n")

	return os.WriteFile(agentsPath, []byte(finalContent), 0644)
}

func makeAgentsBlock(entry *MemoryEntry) agentsBlock {
	header := fmt.Sprintf("- **%s** (%s): %s", entry.Name, entry.Type, entry.Description)
	contentLines := strings.Split(entry.Content, "\n")
	var body []string
	if len(contentLines) > 0 && strings.TrimSpace(entry.Content) != "" {
		body = append(body, "  - *Content*:")
		for _, cl := range contentLines {
			body = append(body, "    "+cl)
		}
	}
	return agentsBlock{
		name:       entry.Name,
		headerLine: header,
		bodyLines:  body,
	}
}

func (mm *MemoryManager) syncFromAgentsMDLocked() error {
	agentsMDMu.Lock()
	defer agentsMDMu.Unlock()

	agentsPath := "AGENTS.md"
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	const sectionTitle = "## Agent Dynamic Learnings"
	content := string(data)
	idx := strings.Index(content, sectionTitle)
	if idx == -1 {
		return nil
	}

	afterSection := content[idx+len(sectionTitle):]
	lines := strings.Split(afterSection, "\n")

	type parsedBlock struct {
		name        string
		memType     MemoryType
		description string
		content     string
	}
	var parsedBlocks []parsedBlock
	var currentBlock *parsedBlock
	var contentLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- **") && strings.Contains(trimmed, "** (") {
			if currentBlock != nil {
				currentBlock.content = strings.TrimSpace(strings.Join(contentLines, "\n"))
				parsedBlocks = append(parsedBlocks, *currentBlock)
				contentLines = nil
			}
			
			nameEnd := strings.Index(trimmed[4:], "**")
			if nameEnd == -1 {
				currentBlock = nil
				continue
			}
			name := trimmed[4 : 4+nameEnd]
			
			rest := strings.TrimSpace(trimmed[4+nameEnd+2:])
			typeEnd := strings.Index(rest, ")")
			if typeEnd == -1 || !strings.HasPrefix(rest, "(") {
				currentBlock = nil
				continue
			}
			tStr := rest[1:typeEnd]
			
			desc := ""
			descIdx := strings.Index(rest[typeEnd:], ": ")
			if descIdx != -1 {
				desc = strings.TrimSpace(rest[typeEnd+descIdx+2:])
			}
			
			currentBlock = &parsedBlock{
				name:        name,
				memType:     MemoryType(tStr),
				description: desc,
			}
		} else if currentBlock != nil && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") || line == "") {
			trimmedLine := strings.TrimSpace(line)
			if trimmedLine == "- *Content*:" || trimmedLine == "*Content*:" {
				continue
			}
			if strings.HasPrefix(line, "    ") {
				contentLines = append(contentLines, line[4:])
			} else if strings.HasPrefix(line, "  ") {
				contentLines = append(contentLines, line[2:])
			} else {
				contentLines = append(contentLines, trimmedLine)
			}
		} else {
			if currentBlock != nil {
				currentBlock.content = strings.TrimSpace(strings.Join(contentLines, "\n"))
				parsedBlocks = append(parsedBlocks, *currentBlock)
				currentBlock = nil
				contentLines = nil
			}
		}
	}
	if currentBlock != nil {
		currentBlock.content = strings.TrimSpace(strings.Join(contentLines, "\n"))
		parsedBlocks = append(parsedBlocks, *currentBlock)
	}

	saveDir, err := projectMemoryDir()
	if err != nil {
		return err
	}

	presentNames := make(map[string]bool)

	for _, pb := range parsedBlocks {
		if !validMemoryTypes[pb.memType] {
			continue
		}
		presentNames[pb.name] = true

		existing, exists := mm.entries[pb.name]

		if !exists || existing.Description != pb.description || existing.Content != pb.content || existing.Type != pb.memType {
			filename := slugify(pb.name) + ".md"
			now := time.Now().UTC()
			entry := &MemoryEntry{
				Name:        pb.name,
				Description: pb.description,
				Type:        pb.memType,
				Content:     pb.content,
				UpdatedAt:   now,
				File:        filename,
			}

			_ = os.MkdirAll(saveDir, 0755)
			filePath := filepath.Join(saveDir, filename)
			text := renderFrontmatter(entry)
			_ = os.WriteFile(filePath, []byte(text), 0600)

			mm.entries[pb.name] = entry
			if len(mm.dirs) == 0 || mm.dirs[len(mm.dirs)-1] != saveDir {
				mm.dirs = append(mm.dirs, saveDir)
			}
		}
	}

	var toDelete []string
	for name, entry := range mm.entries {
		filePath := filepath.Join(saveDir, entry.File)
		if _, err := os.Stat(filePath); err == nil {
			if !presentNames[name] {
				toDelete = append(toDelete, name)
			}
		}
	}

	for _, name := range toDelete {
		_ = mm.deleteLocked(name, true)
	}

	if len(parsedBlocks) > 0 || len(toDelete) > 0 {
		mm.rebuildIndexLocked(saveDir)
	}

	return nil
}

// DreamConsolidator automates persistent memory consolidation (deduplication, pruning, merging).
// It runs when 7 gates are satisfied, utilizing a PID-based file lock (.dream_lock) for multi-session safety.
type DreamConsolidator struct {
	mu                sync.Mutex
	Enabled           bool
	Cooldown          time.Duration
	Throttle          time.Duration
	MinSessions       int
	LastConsolidation time.Time
	LastScan          time.Time
	SessionCount      int
}

// NewDreamConsolidator creates a default DreamConsolidator.
func NewDreamConsolidator() *DreamConsolidator {
	return &DreamConsolidator{
		Enabled:     true,
		Cooldown:    24 * time.Hour,
		Throttle:    10 * time.Minute,
		MinSessions: 5,
	}
}

// IncrementSession increments the session counter.
func (dc *DreamConsolidator) IncrementSession() {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.SessionCount++
}

// isProcessAlive checks if a process with given PID is still active on UNIX/macOS systems.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// sending signal 0 checks for process existence on Unix-like systems
	return proc.Signal(syscall.Signal(0)) == nil
}

// acquireLock tries to write a PID-based file lock .dream_lock in the target directory.
func (dc *DreamConsolidator) acquireLock(dir string) (bool, error) {
	lockPath := filepath.Join(dir, ".dream_lock")
	data, err := os.ReadFile(lockPath)
	if err == nil {
		content := strings.TrimSpace(string(data))
		parts := strings.Split(content, ":")
		if len(parts) == 2 {
			pidVal, _ := strconv.Atoi(parts[0])
			timeVal, _ := strconv.ParseInt(parts[1], 10, 64)

			// Check if lock process is alive
			alive := false
			if pidVal > 0 {
				alive = isProcessAlive(pidVal)
			}

			// If process is still alive and lock is within 1 hour, lock is valid (cannot acquire)
			if alive && time.Since(time.Unix(timeVal, 0)) < 1*time.Hour {
				return false, nil
			}

			// Otherwise, remove stale lock
			_ = os.Remove(lockPath)
		}
	}

	// Write new lock file
	lockContent := fmt.Sprintf("%d:%d", os.Getpid(), time.Now().Unix())
	if err := os.WriteFile(lockPath, []byte(lockContent), 0600); err != nil {
		return false, err
	}
	return true, nil
}

// releaseLock deletes the PID-based .dream_lock file if owned by the current process.
func (dc *DreamConsolidator) releaseLock(dir string) {
	lockPath := filepath.Join(dir, ".dream_lock")
	data, err := os.ReadFile(lockPath)
	if err == nil {
		content := strings.TrimSpace(string(data))
		parts := strings.Split(content, ":")
		if len(parts) > 0 {
			pidVal, _ := strconv.Atoi(parts[0])
			if pidVal == os.Getpid() {
				_ = os.Remove(lockPath)
			}
		}
	}
}

// ShouldConsolidate evaluates the 7 validation gates to see if consolidation is allowed.
// If force is true, Gates 4 (cooldown), 5 (scan throttle), and 6 (session count) are bypassed.
func (dc *DreamConsolidator) ShouldConsolidate(mm *MemoryManager, force bool) (bool, string) {
	if !dc.Enabled {
		return false, "Gate 1: consolidation is disabled"
	}

	saveDir, err := projectMemoryDir()
	if err != nil {
		return false, "Gate 2: failed to resolve memory directory"
	}

	files, err := os.ReadDir(saveDir)
	if err != nil {
		return false, "Gate 2: memory directory does not exist"
	}

	hasMemories := false
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") && f.Name() != "MEMORY.md" && f.Name() != ".dream_lock" {
			hasMemories = true
			break
		}
	}
	if !hasMemories {
		return false, "Gate 2: no memory files found"
	}

	if GlobalPermissionManager.GetMode() == ModePlan {
		return false, "Gate 3: plan mode does not allow consolidation"
	}

	if !force {
		if !dc.LastConsolidation.IsZero() && time.Since(dc.LastConsolidation) < dc.Cooldown {
			remaining := dc.Cooldown - time.Since(dc.LastConsolidation)
			return false, fmt.Sprintf("Gate 4: cooldown active, %v remaining", remaining.Round(time.Second))
		}

		if !dc.LastScan.IsZero() && time.Since(dc.LastScan) < dc.Throttle {
			remaining := dc.Throttle - time.Since(dc.LastScan)
			return false, fmt.Sprintf("Gate 5: scan throttle active, %v remaining", remaining.Round(time.Second))
		}

		if dc.SessionCount < dc.MinSessions {
			return false, fmt.Sprintf("Gate 6: only %d sessions, need %d", dc.SessionCount, dc.MinSessions)
		}
	}

	// Try acquiring PID-based lock (Gate 7)
	locked, err := dc.acquireLock(saveDir)
	if err != nil || !locked {
		return false, "Gate 7: lock held by another process or failed to acquire lock"
	}

	return true, "All 7 gates passed"
}

// Consolidate executes the 4-phase consolidation routine.
func (dc *DreamConsolidator) Consolidate(mm *MemoryManager, force bool) ([]string, error) {
	saveDir, err := projectMemoryDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project memory directory: %w", err)
	}

	dc.LastScan = time.Now()

	// Gate Check
	canRun, reason := dc.ShouldConsolidate(mm, force)
	if !canRun {
		return nil, fmt.Errorf("consolidation blocked: %s", reason)
	}
	defer dc.releaseLock(saveDir)

	var completedPhases []string

	// Phase 1: Orient (Read MEMORY.md index and structure)
	completedPhases = append(completedPhases, "Phase 1: Orient - scan memory directory index")
	mm.Reload()

	// Phase 2: Gather (Load all memory entries and their contents)
	completedPhases = append(completedPhases, "Phase 2: Gather - read individual memory files for full content")
	mm.mu.RLock()
	var entries []*MemoryEntry
	for _, e := range mm.entries {
		entries = append(entries, e)
	}
	mm.mu.RUnlock()

	// Phase 3: Consolidate (Deduplicate identical content or merge duplicate names)
	completedPhases = append(completedPhases, "Phase 3: Consolidate - merge related memories and clean up duplicates")

	// Delete empty content or empty name entries
	var active []*MemoryEntry
	for _, e := range entries {
		if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Content) == "" {
			_ = mm.Delete(e.Name)
		} else {
			active = append(active, e)
		}
	}

	// Exact content deduplication within type groups
	seenContent := make(map[string]*MemoryEntry)
	var unique []*MemoryEntry
	for _, e := range active {
		normContent := strings.ToLower(strings.TrimSpace(e.Content))
		key := string(e.Type) + ":" + normContent
		if existing, found := seenContent[key]; found {
			// Duplicate content, delete the newer one
			toDelete := e
			if e.UpdatedAt.Before(existing.UpdatedAt) {
				toDelete = existing
				seenContent[key] = e
			}
			_ = mm.Delete(toDelete.Name)
			LogAudit(CatSession, "memory_consolidate_dedup", fmt.Sprintf("Removed duplicate memory '%s' in favor of '%s'", toDelete.Name, seenContent[key].Name), nil)
		} else {
			seenContent[key] = e
			unique = append(unique, e)
		}
	}

	// Phase 4: Prune (Enforce maximum memory entries cap)
	completedPhases = append(completedPhases, "Phase 4: Prune - enforce max memory entries and rebuild index")

	// Sort by UpdatedAt ascending (oldest first)
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].UpdatedAt.Before(unique[j].UpdatedAt)
	})

	// If we exceed cap, prune oldest entries
	maxCap := MaxMemoryEntries
	if len(unique) > maxCap {
		pruneCount := len(unique) - maxCap
		for i := 0; i < pruneCount; i++ {
			_ = mm.Delete(unique[i].Name)
			LogAudit(CatSession, "memory_consolidate_prune", fmt.Sprintf("Pruned oldest memory entry '%s' to stay under cap", unique[i].Name), nil)
		}
	}

	// Rebuild index and save
	mm.Reload()
	dc.LastConsolidation = time.Now()
	return completedPhases, nil
}

