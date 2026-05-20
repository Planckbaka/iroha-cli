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
	if !strings.Contains(prompt, "你是一个专业的软件工程助手，名叫 go-claude") {
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
	tempDir, err := os.MkdirTemp("", "go-claude-test-*")
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
	tempDir, err := os.MkdirTemp("", "go-claude-test-skills-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a mock .go-claude/skills folder
	skillsDir := filepath.Join(tempDir, ".go-claude", "skills")
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
