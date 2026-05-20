package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
//	~/.go-claude/memory/        global memories
//	  MEMORY.md                 index file (auto-generated)
//	  prefer_pnpm.md            one .md per entry
//
//	./.go-claude/memory/        project-level memories (merged on top)
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
	dirs    []string               // loaded directory paths (for display)
}

// GlobalMemoryManager is the singleton used throughout the session.
var GlobalMemoryManager = NewMemoryManager()

// NewMemoryManager creates a MemoryManager and loads existing memories from disk.
func NewMemoryManager() *MemoryManager {
	mm := &MemoryManager{
		entries: make(map[string]*MemoryEntry),
	}
	mm.load()
	return mm
}

// Reload discards all in-memory entries and re-reads from disk.
func (mm *MemoryManager) Reload() {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.entries = make(map[string]*MemoryEntry)
	mm.dirs = nil
	mm.load()
}

func (mm *MemoryManager) load() {
	// Layer 1: global memories
	if home, err := os.UserHomeDir(); err == nil {
		mm.loadDir(filepath.Join(home, ".go-claude", "memory"))
	}
	// Layer 2: project memories (merged on top; same name overwrites global)
	if cwd, err := os.Getwd(); err == nil {
		mm.loadDir(filepath.Join(cwd, ".go-claude", "memory"))
	}
}

func (mm *MemoryManager) loadDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // directory absent — silently skip
	}
	loaded := 0
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") || de.Name() == "MEMORY.md" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			continue
		}
		entry, err := parseFrontmatter(string(data))
		if err != nil || entry == nil {
			continue
		}
		entry.File = de.Name()
		mm.entries[entry.Name] = entry
		loaded++
	}
	if loaded > 0 {
		mm.dirs = append(mm.dirs, dir)
	}
}

// Save writes a memory entry to disk and updates the in-memory store.
// If an entry with the same name already exists it is overwritten.
func (mm *MemoryManager) Save(name, description string, memType MemoryType, content string) error {
	if !validMemoryTypes[memType] {
		return fmt.Errorf("invalid memory type %q: must be one of user, feedback, project, reference", memType)
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("memory name cannot be empty")
	}

	// Determine save directory: prefer project-level
	saveDir, err := projectMemoryDir()
	if err != nil {
		return fmt.Errorf("cannot determine memory directory: %w", err)
	}
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return fmt.Errorf("cannot create memory directory: %w", err)
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

	text := renderFrontmatter(entry)
	if err := os.WriteFile(filepath.Join(saveDir, filename), []byte(text), 0644); err != nil {
		return fmt.Errorf("cannot write memory file: %w", err)
	}

	mm.mu.Lock()
	mm.entries[name] = entry
	if len(mm.dirs) == 0 || mm.dirs[len(mm.dirs)-1] != saveDir {
		mm.dirs = append(mm.dirs, saveDir)
	}
	mm.mu.Unlock()

	mm.rebuildIndex(saveDir)
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
func (mm *MemoryManager) BuildSystemPromptSection() string {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	if len(mm.entries) == 0 {
		return ""
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

	for _, t := range typeOrder {
		var typed []*MemoryEntry
		for _, e := range mm.entries {
			if e.Type == t {
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
	}

	return sb.String()
}

// rebuildIndex rewrites the MEMORY.md index file in the given directory.
func (mm *MemoryManager) rebuildIndex(dir string) {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	var lines []string
	lines = append(lines, "# Memory Index", "")
	for _, e := range mm.entries {
		lines = append(lines, fmt.Sprintf("- %s: %s [%s]", e.Name, e.Description, e.Type))
	}
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(strings.Join(lines, "\n")+"\n"), 0644)
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

// projectMemoryDir returns ./.go-claude/memory (creating if needed).
func projectMemoryDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".go-claude", "memory"), nil
}
