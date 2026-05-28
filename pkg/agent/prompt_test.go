package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptBuilderBasic(t *testing.T) {
	// Initialize a builder
	builder := NewSystemPromptBuilder()
	prompt := builder.Build()

	// 1. Check core persona
	if !strings.Contains(prompt, "You are Iroha, a professional software engineering assistant") {
		t.Errorf("expected prompt to contain core persona, got: %s", prompt)
	}

	// 2. Check caching boundary
	if !strings.Contains(prompt, "=== DYNAMIC_BOUNDARY ===") {
		t.Errorf("expected prompt to contain caching boundary, got: %s", prompt)
	}

	// 3. Check dynamic context
	if !strings.Contains(prompt, "Current Local Time:") {
		t.Errorf("expected prompt to contain local time, got: %s", prompt)
	}
	if !strings.Contains(prompt, "Current Working Directory:") {
		t.Errorf("expected prompt to contain working directory, got: %s", prompt)
	}
	if !strings.Contains(prompt, "Active Safety Mode:") {
		t.Errorf("expected prompt to contain safety mode, got: %s", prompt)
	}
}

func TestPromptBuilderDynamicContext(t *testing.T) {
	builder := NewSystemPromptBuilder()

	// Set a specific security mode
	origMode := GlobalPermissionManager.GetMode()
	defer func() { _ = GlobalPermissionManager.SetMode(origMode) }()

	err := GlobalPermissionManager.SetMode(ModePlan)
	if err != nil {
		t.Fatalf("failed to set safety mode: %v", err)
	}

	prompt := builder.Build()
	if !strings.Contains(prompt, "Active Safety Mode: plan") {
		t.Errorf("expected prompt to contain updated safety mode 'plan', got: %s", prompt)
	}

	err = GlobalPermissionManager.SetMode(ModeAuto)
	if err != nil {
		t.Fatalf("failed to set safety mode: %v", err)
	}

	prompt2 := builder.Build()
	if !strings.Contains(prompt2, "Active Safety Mode: auto") {
		t.Errorf("expected prompt to contain updated safety mode 'auto', got: %s", prompt2)
	}
}

func TestPromptBuilderLayeredCLAUDE(t *testing.T) {
	// Create a temp workspace directory for testing
	tempDir, err := os.MkdirTemp("", "iroha-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create project-level CLAUDE.md
	projCLAUDE := filepath.Join(tempDir, "CLAUDE.md")
	projContent := "Build: go build\nTest: go test ./..."
	if err := os.WriteFile(projCLAUDE, []byte(projContent), 0644); err != nil {
		t.Fatalf("failed to write proj CLAUDE: %v", err)
	}

	// Create a sub-directory representing current working directory
	subDir := filepath.Join(tempDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}

	// Create cwd-level CLAUDE.md
	cwdCLAUDE := filepath.Join(subDir, "CLAUDE.md")
	cwdContent := "Local rules: only format with gofmt"
	if err := os.WriteFile(cwdCLAUDE, []byte(cwdContent), 0644); err != nil {
		t.Fatalf("failed to write cwd CLAUDE: %v", err)
	}

	// Also create a dummy go.mod at the tempDir so findProjectRoot resolves tempDir as project root
	dummyMod := filepath.Join(tempDir, "go.mod")
	if err := os.WriteFile(dummyMod, []byte("module test"), 0644); err != nil {
		t.Fatalf("failed to write dummy go.mod: %v", err)
	}

	// Initialize builder pointing to the subDir CWD
	builder := &SystemPromptBuilder{
		workdir: subDir,
	}

	prompt := builder.Build()

	// Verify project guideline was read
	if !strings.Contains(prompt, "Project Guideline") || !strings.Contains(prompt, projContent) {
		t.Errorf("expected prompt to contain project-level CLAUDE.md, got: %s", prompt)
	}

	// Verify CWD guideline was read
	if !strings.Contains(prompt, "Current Directory Guideline") || !strings.Contains(prompt, cwdContent) {
		t.Errorf("expected prompt to contain cwd-level CLAUDE.md, got: %s", prompt)
	}
}

func TestPromptBuilderSkills(t *testing.T) {
	// Create a temp workspace directory for testing
	tempDir, err := os.MkdirTemp("", "iroha-test-skills-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a mock .iroha/skills folder
	skillsDir := filepath.Join(tempDir, ".iroha", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("failed to create skills dir: %v", err)
	}

	// Create a skill file
	skillPath := filepath.Join(skillsDir, "minify.md")
	skillContent := "Rule: always minify JS outputs using terser."
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write skill file: %v", err)
	}

	// Dummy go.mod so project root is tempDir
	dummyMod := filepath.Join(tempDir, "go.mod")
	if err := os.WriteFile(dummyMod, []byte("module test"), 0644); err != nil {
		t.Fatalf("failed to write dummy go.mod: %v", err)
	}

	builder := &SystemPromptBuilder{
		workdir: tempDir,
	}

	prompt := builder.Build()

	// Verify that custom skill is loaded in prompt
	if !strings.Contains(prompt, "Active Custom Skills") || !strings.Contains(prompt, "minify") || !strings.Contains(prompt, skillContent) {
		t.Errorf("expected prompt to contain custom skill, got: %s", prompt)
	}
}

func TestPromptBuilderTeammatesAndWorktrees(t *testing.T) {
	// 1. Setup mock teammate
	_, err := GlobalTeamManager.RegisterTeammate("PromptSpecialist", "Reviewing prompts", "Custom sys prompt", "executor")
	if err != nil {
		t.Fatalf("failed to register teammate: %v", err)
	}
	defer func() {
		// Clean up teammate config
		configPath := filepath.Join(GlobalTeamManager.teamDir, "config.json")
		os.Remove(configPath)
	}()

	// 2. Setup mock worktree
	GlobalWorktreeManager.entries["TestWT"] = &WorktreeEntry{
		Name:   "TestWT",
		Branch: "wt/TestWT",
		TaskID: "task-999",
		Status: "active",
		Path:   "/path/to/TestWT",
	}
	defer delete(GlobalWorktreeManager.entries, "TestWT")

	builder := NewSystemPromptBuilder()

	// 3. Test prompt with compressed message count (< 3)
	origCount := GlobalMessageCount
	defer func() { GlobalMessageCount = origCount }()

	GlobalMessageCount = 2
	promptComp := builder.Build()
	if !strings.Contains(promptComp, "<identity>") {
		t.Errorf("expected identity tags in prompt under message compression, got: %s", promptComp)
	}

	GlobalMessageCount = 5
	// Use a fresh builder so section hashes don't cache the teammates/worktrees
	promptNormal := NewSystemPromptBuilder().Build()
	if strings.Contains(promptNormal, "<identity>") {
		t.Errorf("did not expect identity tags in prompt under normal message count, got: %s", promptNormal)
	}

	// 4. Test teammate roster injection
	if !strings.Contains(promptNormal, "PromptSpecialist") || !strings.Contains(promptNormal, "Reviewing prompts") {
		t.Errorf("expected prompt to contain teammate info, got: %s", promptNormal)
	}

	// 5. Test worktree branch injection
	if !strings.Contains(promptNormal, "TestWT") || !strings.Contains(promptNormal, "wt/TestWT") {
		t.Errorf("expected prompt to contain worktree info, got: %s", promptNormal)
	}
}

func TestPromptBuilderLayeredAGENTSAndSKILLFolder(t *testing.T) {
	// Create a temp workspace directory for testing
	tempDir, err := os.MkdirTemp("", "iroha-test-agents-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create project-level AGENTS.md
	projAGENTS := filepath.Join(tempDir, "AGENTS.md")
	projContent := "Build: go build AGENTS\nTest: go test AGENTS"
	if err := os.WriteFile(projAGENTS, []byte(projContent), 0644); err != nil {
		t.Fatalf("failed to write proj AGENTS: %v", err)
	}

	// Create a sub-directory representing current working directory
	subDir := filepath.Join(tempDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}

	// Create cwd-level AGENTS.md
	cwdAGENTS := filepath.Join(subDir, "AGENTS.md")
	cwdContent := "Local rules: only format agents with gofmt"
	if err := os.WriteFile(cwdAGENTS, []byte(cwdContent), 0644); err != nil {
		t.Fatalf("failed to write cwd AGENTS: %v", err)
	}

	// Create a recursive skill folder: tempDir/.iroha/skills/my-recursive-skill/SKILL.md
	// (at project root so findProjectRoot resolves tempDir, not subDir)
	skillDir := filepath.Join(tempDir, ".iroha", "skills", "my-recursive-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create recursive skill dir: %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	skillContent := "Recursive skill instruction details."
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	// Also create a dummy go.mod at the tempDir so findProjectRoot resolves tempDir as project root
	dummyMod := filepath.Join(tempDir, "go.mod")
	if err := os.WriteFile(dummyMod, []byte("module test"), 0644); err != nil {
		t.Fatalf("failed to write dummy go.mod: %v", err)
	}

	// Initialize builder pointing to the subDir CWD
	builder := &SystemPromptBuilder{
		workdir: subDir,
	}

	prompt := builder.Build()

	// Verify project guideline was read
	if !strings.Contains(prompt, "AGENTS.md Guidelines") || !strings.Contains(prompt, projContent) {
		t.Errorf("expected prompt to contain project-level AGENTS.md, got: %s", prompt)
	}

	// Verify CWD guideline was read
	if !strings.Contains(prompt, cwdContent) {
		t.Errorf("expected prompt to contain cwd-level AGENTS.md, got: %s", prompt)
	}

	// Verify recursive skill SKILL.md was read
	if !strings.Contains(prompt, "Active Custom Skills") || !strings.Contains(prompt, "my-recursive-skill") || !strings.Contains(prompt, skillContent) {
		t.Errorf("expected prompt to contain recursive skill SKILL.md, got: %s", prompt)
	}
}

func TestHashSection_Deterministic(t *testing.T) {
	b := NewSystemPromptBuilder()
	content := "test content for hashing"
	h1 := b.hashSection(content)
	h2 := b.hashSection(content)
	if h1 != h2 {
		t.Errorf("hashSection should be deterministic: got %q then %q", h1, h2)
	}
	if len(h1) != 16 {
		t.Errorf("expected 16-char hash, got %d chars: %q", len(h1), h1)
	}
}

func TestHashSection_DifferentInputs(t *testing.T) {
	b := NewSystemPromptBuilder()
	h1 := b.hashSection("content A")
	h2 := b.hashSection("content B")
	if h1 == h2 {
		t.Error("different inputs should produce different hashes")
	}
}

func TestMarkStale(t *testing.T) {
	b := NewSystemPromptBuilder()
	b.sectionHashes["section_a"] = "hash1"
	b.sectionHashes["section_b"] = "hash2"

	b.MarkStale("section_a")

	if _, ok := b.sectionHashes["section_a"]; ok {
		t.Error("expected section_a to be removed from hashes")
	}
	if _, ok := b.sectionHashes["section_b"]; !ok {
		t.Error("expected section_b to still exist in hashes")
	}

	// Mark non-existent section should not panic
	b.MarkStale("non_existent")
}

func TestMaybeCached_Uncached(t *testing.T) {
	b := NewSystemPromptBuilder()
	content := "full section content"
	hash := b.hashSection(content)

	result := b.maybeCached("test_section", content, hash)
	if result != content {
		t.Errorf("uncached section should return full content, got: %q", result)
	}
}

func TestMaybeCached_Cached(t *testing.T) {
	b := NewSystemPromptBuilder()
	content := "full section content"
	hash := b.hashSection(content)

	// Simulate a prior call that stored the hash
	b.sectionHashes["test_section"] = hash

	result := b.maybeCached("test_section", content, hash)
	if !strings.Contains(result, "cached:") {
		t.Errorf("cached section should return cached marker, got: %q", result)
	}
	if strings.Contains(result, content) {
		t.Error("cached section should NOT contain full content")
	}
}

func TestMaybeCached_HashChanged(t *testing.T) {
	b := NewSystemPromptBuilder()
	b.sectionHashes["test_section"] = "old_hash"

	content := "new content"
	newHash := b.hashSection(content)

	result := b.maybeCached("test_section", content, newHash)
	if result != content {
		t.Errorf("changed hash should return full content, got: %q", result)
	}
}

func TestFindProjectRoot(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(tmpDir string) string // returns workdir
		wantRoot  func(tmpDir string) string // returns expected root
	}{
		{
			"git_marker",
			func(tmpDir string) string {
				os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
				os.WriteFile(filepath.Join(tmpDir, ".git"), []byte(""), 0644)
				return filepath.Join(tmpDir, "sub")
			},
			func(tmpDir string) string { return tmpDir },
		},
		{
			"go_mod_marker",
			func(tmpDir string) string {
				os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
				os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0644)
				return filepath.Join(tmpDir, "sub")
			},
			func(tmpDir string) string { return tmpDir },
		},
		{
			"iroha_marker",
			func(tmpDir string) string {
				os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
				os.MkdirAll(filepath.Join(tmpDir, ".iroha"), 0755)
				return filepath.Join(tmpDir, "sub")
			},
			func(tmpDir string) string { return tmpDir },
		},
		{
			"go_claude_marker",
			func(tmpDir string) string {
				os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
				os.MkdirAll(filepath.Join(tmpDir, ".go-claude"), 0755)
				return filepath.Join(tmpDir, "sub")
			},
			func(tmpDir string) string { return tmpDir },
		},
		{
			"no_marker_fallback",
			func(tmpDir string) string {
				os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
				return filepath.Join(tmpDir, "sub")
			},
			func(tmpDir string) string { return filepath.Join(tmpDir, "sub") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "iroha-project-root-*")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			workdir := tt.setup(tmpDir)
			expected := tt.wantRoot(tmpDir)

			got := findProjectRoot(workdir)
			if got != expected {
				t.Errorf("findProjectRoot(%q) = %q, want %q", workdir, got, expected)
			}
		})
	}
}

func TestGetUniqueSkillDirs_Deduplication(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-skill-dirs-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .iroha/skills and .go-claude/skills at same location
	os.MkdirAll(filepath.Join(tmpDir, ".iroha", "skills"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0644)

	dirs := getUniqueSkillDirs(tmpDir)

	seen := make(map[string]int)
	for _, d := range dirs {
		seen[d]++
	}
	for d, count := range seen {
		if count > 1 {
			t.Errorf("duplicate dir found: %s (count %d)", d, count)
		}
	}
}

func TestReadSkills_SkipsNonMdFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-skills-nonmd-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, ".iroha", "skills")
	os.MkdirAll(skillsDir, 0755)
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0644)

	// Create non-.md file
	os.WriteFile(filepath.Join(skillsDir, "config.yaml"), []byte("key: value"), 0644)
	// Create a valid .md file
	os.WriteFile(filepath.Join(skillsDir, "valid.md"), []byte("Valid skill content"), 0644)

	b := &SystemPromptBuilder{workdir: tmpDir, sectionHashes: make(map[string]string)}
	result := b.readSkills()

	if !strings.Contains(result, "valid") {
		t.Error("expected valid.md skill to be loaded")
	}
	if strings.Contains(result, "config.yaml") {
		t.Error("non-.md files should be skipped")
	}
}

func TestReadSkills_SkipsSubdirsWithoutSkillMd(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-skills-noskillmd-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, ".iroha", "skills")
	os.MkdirAll(filepath.Join(skillsDir, "empty-subdir"), 0755)
	os.MkdirAll(filepath.Join(skillsDir, "with-skill"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0644)
	os.WriteFile(filepath.Join(skillsDir, "with-skill", "SKILL.md"), []byte("Nested skill content"), 0644)

	b := &SystemPromptBuilder{workdir: tmpDir, sectionHashes: make(map[string]string)}
	result := b.readSkills()

	if !strings.Contains(result, "with-skill") {
		t.Error("expected subdir with SKILL.md to be loaded")
	}
	if strings.Contains(result, "empty-subdir") {
		t.Error("empty subdirs should be skipped")
	}
}

func TestBuildWithPrompt_ContainsDynamicBoundary(t *testing.T) {
	b := NewSystemPromptBuilder()
	prompt := b.BuildWithPrompt("test prompt")
	if !strings.Contains(prompt, "=== DYNAMIC_BOUNDARY ===") {
		t.Error("expected DYNAMIC_BOUNDARY in BuildWithPrompt output")
	}
	if !strings.Contains(prompt, "Current Local Time:") {
		t.Error("expected time section in dynamic boundary")
	}
}

func TestBuild_DelegatesToBuildWithPrompt(t *testing.T) {
	b := NewSystemPromptBuilder()
	result := b.Build()
	if result == "" {
		t.Error("Build() should return non-empty string")
	}
	if !strings.Contains(result, "You are Iroha") {
		t.Error("Build() should contain core persona")
	}
}
