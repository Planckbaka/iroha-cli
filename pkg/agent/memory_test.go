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

	// The file should exist in .iroha/memory/
	expectedFile := filepath.Join(dir, ".iroha", "memory", "no_snapshot_edits.md")
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

	indexFile := filepath.Join(dir, ".iroha", "memory", "MEMORY.md")
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

func TestMemoryManager_BuildSystemPromptSection_Filtering(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	_ = mm.Save("prefer_pnpm", "User prefers pnpm", MemTypeUser, "Always use pnpm.")
	_ = mm.Save("no_snapshots", "Do not edit snapshots", MemTypeFeedback, "Never edit snapshots.")
	_ = mm.Save("legacy_dir", "Deployment dependencies in legacy directory", MemTypeProject, "Do not delete.")

	// Case 1: Empty filter prompt -> should inject everything
	allSec := mm.BuildSystemPromptSection()
	if !strings.Contains(allSec, "prefer_pnpm") || !strings.Contains(allSec, "no_snapshots") || !strings.Contains(allSec, "legacy_dir") {
		t.Error("expected all memories to be injected when no prompt is provided")
	}

	// Case 2: Match pnpm -> should inject feedback (always) and prefer_pnpm, but NOT legacy_dir
	pnpmSec := mm.BuildSystemPromptSection("I want to run a pnpm build command")
	if !strings.Contains(pnpmSec, "no_snapshots") {
		t.Error("expected feedback memory to be injected always")
	}
	if !strings.Contains(pnpmSec, "prefer_pnpm") {
		t.Error("expected matched prefer_pnpm to be injected")
	}
	if strings.Contains(pnpmSec, "legacy_dir") {
		t.Error("expected unmatched legacy_dir to be filtered out")
	}

	// Case 3: Match legacy -> should inject feedback and legacy_dir, but NOT prefer_pnpm
	legacySec := mm.BuildSystemPromptSection("Clean up the legacy build folders")
	if !strings.Contains(legacySec, "no_snapshots") {
		t.Error("expected feedback memory to be injected always")
	}
	if !strings.Contains(legacySec, "legacy_dir") {
		t.Error("expected matched legacy_dir to be injected")
	}
	if strings.Contains(legacySec, "prefer_pnpm") {
		t.Error("expected unmatched prefer_pnpm to be filtered out")
	}
}

func TestMemoryManager_SyncToAgentsMD(t *testing.T) {
	dir := t.TempDir()
	mm := newMemoryManagerInDir(t, dir)

	// Case 1: Saving a memory should create AGENTS.md and populate ## Agent Dynamic Learnings
	err := mm.Save("prefer_pnpm", "User prefers pnpm", MemTypeUser, "Always use pnpm.")
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	data, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("AGENTS.md should be created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "## Agent Dynamic Learnings") {
		t.Error("AGENTS.md should contain the dynamic learnings section header")
	}
	if !strings.Contains(content, "- **prefer_pnpm** (user): User prefers pnpm") {
		t.Error("AGENTS.md should contain the saved entry name, type, and description")
	}
	if !strings.Contains(content, "Always use pnpm.") {
		t.Error("AGENTS.md should contain the entry content")
	}

	// Case 2: Updating the memory should replace it under ## Agent Dynamic Learnings
	err = mm.Update("prefer_pnpm", "User strongly prefers pnpm", MemTypeUser, "Use pnpm for everything.")
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	data2, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("Failed to read updated AGENTS.md: %v", err)
	}
	content2 := string(data2)
	if strings.Contains(content2, "User prefers pnpm") {
		t.Error("AGENTS.md should not contain the old description after update")
	}
	if !strings.Contains(content2, "- **prefer_pnpm** (user): User strongly prefers pnpm") {
		t.Error("AGENTS.md should contain the updated description")
	}
	if !strings.Contains(content2, "Use pnpm for everything.") {
		t.Error("AGENTS.md should contain the updated content")
	}

	// Case 3: Deleting the memory should remove it from AGENTS.md
	err = mm.Delete("prefer_pnpm")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	data3, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("Failed to read AGENTS.md after delete: %v", err)
	}
	content3 := string(data3)
	if strings.Contains(content3, "prefer_pnpm") {
		t.Error("AGENTS.md should not contain deleted memory 'prefer_pnpm'")
	}
}

func TestMemoryManager_SyncFromAgentsMD(t *testing.T) {
	dir := t.TempDir()
	
	// Create a pre-existing AGENTS.md with two memories
	agentsContent := `# iroha-code

## Purpose
Some purpose text.

## Agent Dynamic Learnings
- **yarn_prefer** (user): User prefers yarn over npm
  - *Content*:
    Always run yarn commands.
- **temp_fact** (project): A temporary project fact
  - *Content*:
    This is a temporary fact.
`
	
	original, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	err := os.WriteFile("AGENTS.md", []byte(agentsContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write mock AGENTS.md: %v", err)
	}

	// Loading memory manager should parse and populate both
	mm := NewMemoryManager()
	if mm.Count() != 2 {
		t.Fatalf("expected 2 entries parsed from AGENTS.md, got %d", mm.Count())
	}

	e1, ok := mm.entries["yarn_prefer"]
	if !ok || e1.Description != "User prefers yarn over npm" || e1.Content != "Always run yarn commands." || e1.Type != MemTypeUser {
		t.Errorf("yarn_prefer entry incorrect: %+v", e1)
	}

	e2, ok := mm.entries["temp_fact"]
	if !ok || e2.Description != "A temporary project fact" || e2.Content != "This is a temporary fact." || e2.Type != MemTypeProject {
		t.Errorf("temp_fact entry incorrect: %+v", e2)
	}

	// Update AGENTS.md: remove temp_fact, change yarn_prefer description
	newAgentsContent := `# iroha-code

## Agent Dynamic Learnings
- **yarn_prefer** (user): User strongly prefers yarn over npm
  - *Content*:
    Always run yarn commands.
`
	err = os.WriteFile("AGENTS.md", []byte(newAgentsContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write updated mock AGENTS.md: %v", err)
	}

	// Reloading memory manager should update yarn_prefer and delete temp_fact
	mm.Reload()
	if mm.Count() != 1 {
		t.Fatalf("expected 1 entry after reload/sync, got %d", mm.Count())
	}

	e1Updated, ok := mm.entries["yarn_prefer"]
	if !ok || e1Updated.Description != "User strongly prefers yarn over npm" {
		t.Errorf("yarn_prefer entry not updated correctly: %+v", e1Updated)
	}

	if _, exists := mm.entries["temp_fact"]; exists {
		t.Error("temp_fact should have been deleted during reload sync")
	}
}
