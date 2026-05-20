package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── Save & Load round-trip ───────────────────────────────────────────────

func TestMemoryManager_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	err := mm.Save("prefer_pnpm", "User prefers pnpm over npm", MemTypeUser, "Always use pnpm for package management.")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if mm.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", mm.Count())
	}

	entries := mm.List()
	userEntries := entries[MemTypeUser]
	if len(userEntries) != 1 {
		t.Fatalf("expected 1 user entry, got %d", len(userEntries))
	}
	e := userEntries[0]
	if e.Name != "prefer_pnpm" {
		t.Errorf("expected name 'prefer_pnpm', got %q", e.Name)
	}
	if e.Description != "User prefers pnpm over npm" {
		t.Errorf("unexpected description: %q", e.Description)
	}
	if !strings.Contains(e.Content, "pnpm") {
		t.Errorf("content should contain 'pnpm', got %q", e.Content)
	}
}

// ─── File persistence ─────────────────────────────────────────────────────

func TestMemoryManager_FileIsWrittenToDisk(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	_ = mm.Save("no_snapshot_edits", "Do not edit snapshots", MemTypeFeedback, "Never modify test snapshots unless asked.")

	// The file should exist in .go-claude/memory/
	expectedFile := filepath.Join(dir, ".go-claude", "memory", "no_snapshot_edits.md")
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("expected file %q to exist: %v", expectedFile, err)
	}

	content := string(data)
	if !strings.Contains(content, "name: no_snapshot_edits") {
		t.Errorf("file should contain frontmatter name, got:\n%s", content)
	}
	if !strings.Contains(content, "type: feedback") {
		t.Errorf("file should contain frontmatter type, got:\n%s", content)
	}
}

// ─── MEMORY.md index is rebuilt ──────────────────────────────────────────

func TestMemoryManager_IndexIsRebuilt(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	_ = mm.Save("incident_board", "Incident dashboard URL", MemTypeReference, "https://dash.example.com/incidents")
	_ = mm.Save("legacy_dir_constraint", "Legacy dir cannot be deleted", MemTypeProject, "deployment depends on it")

	indexFile := filepath.Join(dir, ".go-claude", "memory", "MEMORY.md")
	data, err := os.ReadFile(indexFile)
	if err != nil {
		t.Fatalf("MEMORY.md not written: %v", err)
	}
	idx := string(data)
	if !strings.Contains(idx, "incident_board") {
		t.Errorf("MEMORY.md should list 'incident_board', got:\n%s", idx)
	}
	if !strings.Contains(idx, "legacy_dir_constraint") {
		t.Errorf("MEMORY.md should list 'legacy_dir_constraint', got:\n%s", idx)
	}
}

// ─── Overwrite (same name) ────────────────────────────────────────────────

func TestMemoryManager_OverwriteSameName(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	_ = mm.Save("coding_style", "Prefers tabs", MemTypeUser, "Use tabs for indentation.")
	_ = mm.Save("coding_style", "Prefers spaces now", MemTypeUser, "Use 4 spaces for indentation.")

	if mm.Count() != 1 {
		t.Errorf("overwrite should keep count at 1, got %d", mm.Count())
	}
	entries := mm.List()[MemTypeUser]
	if len(entries) != 1 {
		t.Fatalf("expected 1 user entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Content, "4 spaces") {
		t.Errorf("expected updated content, got %q", entries[0].Content)
	}
}

// ─── Invalid type rejected ────────────────────────────────────────────────

func TestMemoryManager_InvalidTypeFails(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	err := mm.Save("bad_entry", "Bad type", "not_a_type", "content")
	if err == nil {
		t.Error("expected error for invalid memory type")
	}
}

// ─── Empty name rejected ──────────────────────────────────────────────────

func TestMemoryManager_EmptyNameFails(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	err := mm.Save("", "No name", MemTypeProject, "content")
	if err == nil {
		t.Error("expected error for empty name")
	}
}

// ─── BuildSystemPromptSection ─────────────────────────────────────────────

func TestMemoryManager_BuildSystemPromptSection(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	// Empty → returns ""
	if s := mm.BuildSystemPromptSection(); s != "" {
		t.Errorf("empty manager should return empty prompt section, got %q", s)
	}

	_ = mm.Save("prefer_pnpm", "User prefers pnpm", MemTypeUser, "Use pnpm always.")
	_ = mm.Save("no_snapshots", "Do not edit snapshots", MemTypeFeedback, "Never edit snapshots.")

	section := mm.BuildSystemPromptSection()
	if !strings.Contains(section, "## Persistent Memories") {
		t.Error("section should have heading")
	}
	if !strings.Contains(section, "prefer_pnpm") {
		t.Error("section should contain user memory")
	}
	if !strings.Contains(section, "no_snapshots") {
		t.Error("section should contain feedback memory")
	}
}

// ─── Reload from disk ─────────────────────────────────────────────────────

func TestMemoryManager_ReloadFromDisk(t *testing.T) {
	dir := t.TempDir()

	// Write one memory using manager1
	mm1 := newMemoryManagerInDir(t, dir)
	_ = mm1.Save("api_endpoint", "Main API base URL", MemTypeReference, "https://api.example.com")

	// Create a fresh manager pointing to the same dir — should load what mm1 wrote
	mm2 := newMemoryManagerInDir(t, dir)
	if mm2.Count() != 1 {
		t.Errorf("expected 1 entry after reload, got %d", mm2.Count())
	}
	entries := mm2.List()[MemTypeReference]
	if len(entries) != 1 || entries[0].Name != "api_endpoint" {
		t.Errorf("expected 'api_endpoint' reference entry, got %+v", entries)
	}
}

// ─── Frontmatter round-trip ───────────────────────────────────────────────

func TestParseFrontmatter_RoundTrip(t *testing.T) {
	entry := &MemoryEntry{
		Name:        "test_entry",
		Description: "A test memory",
		Type:        MemTypeProject,
		Content:     "Some project fact.",
	}
	text := renderFrontmatter(entry)
	parsed, err := parseFrontmatter(text)
	if err != nil {
		t.Fatalf("parseFrontmatter failed: %v", err)
	}
	if parsed.Name != entry.Name {
		t.Errorf("expected name %q, got %q", entry.Name, parsed.Name)
	}
	if parsed.Type != entry.Type {
		t.Errorf("expected type %q, got %q", entry.Type, parsed.Type)
	}
	if parsed.Content != entry.Content {
		t.Errorf("expected content %q, got %q", entry.Content, parsed.Content)
	}
}

// ─── Slugify ─────────────────────────────────────────────────────────────

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"prefer pnpm", "prefer_pnpm"},
		{"No SNAPSHOT edits!", "no_snapshot_edits"},
		{"API-endpoint", "api_endpoint"},
		{"", "memory"},
	}
	for _, c := range cases {
		got := slugify(c.in)
		if got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ─── Helper: newMemoryManagerInDir ───────────────────────────────────────
// Creates a MemoryManager that uses dir as its working directory.
func newMemoryManagerInDir(t *testing.T, dir string) *MemoryManager {
	t.Helper()
	original, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	return NewMemoryManager()
}
