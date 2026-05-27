package agent

import (
	"fmt"
	"regexp"
	"strings"
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

// MaxMemoryEntries caps the total number of stored memory entries.
const MaxMemoryEntries = 100

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
