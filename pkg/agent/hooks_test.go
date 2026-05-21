package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHooksConfig writes a HookConfig to a temp file and returns its path.
func writeHooksConfig(t *testing.T, dir string, cfg HookConfig) string {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	hooksDir := filepath.Join(dir, ".iroha")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(hooksDir, "hooks.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// newManagerFromDir creates a HookManager that reads config from a specific dir.
// We temporarily change the working directory so that the project-level
// config is discovered from the given directory.
func newManagerFromDir(t *testing.T, dir string) *HookManager {
	t.Helper()
	original, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	return NewHookManager()
}

// ─── Exit Code 0: continue ────────────────────────────────────────────────

func TestHookManager_Exit0_Continue(t *testing.T) {
	dir := t.TempDir()
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: "exit 0"},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	result := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})

	if result.Blocked {
		t.Errorf("expected not blocked, got Blocked=true (reason: %q)", result.BlockReason)
	}
	if len(result.Messages) != 0 {
		t.Errorf("expected no messages, got %v", result.Messages)
	}
}

// ─── Exit Code 1: block ────────────────────────────────────────────────────

func TestHookManager_Exit1_Block(t *testing.T) {
	dir := t.TempDir()
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: "echo 'dangerous command detected' >&2; exit 1"},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	result := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})

	if !result.Blocked {
		t.Error("expected Blocked=true")
	}
	if !strings.Contains(result.BlockReason, "dangerous") {
		t.Errorf("expected block reason to contain 'dangerous', got %q", result.BlockReason)
	}
}

// ─── Exit Code 2: inject ──────────────────────────────────────────────────

func TestHookManager_Exit2_Inject(t *testing.T) {
	dir := t.TempDir()
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PostToolUse": {
				{Command: "echo 'lint: no issues found' >&2; exit 2"},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	result := hm.RunHooks(HookPostToolUse, HookContext{ToolName: "file_write", ToolOutput: "ok"})

	if result.Blocked {
		t.Error("exit 2 should not block")
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 injected message, got %d", len(result.Messages))
	}
	if !strings.Contains(result.Messages[0], "lint") {
		t.Errorf("expected message to contain 'lint', got %q", result.Messages[0])
	}
}

// ─── Matcher filter ───────────────────────────────────────────────────────

func TestHookManager_Matcher_FiltersCorrectly(t *testing.T) {
	dir := t.TempDir()
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				// Only blocks shell_run, not file_read
				{Matcher: "shell_run", Command: "exit 1"},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	// should block shell_run
	r1 := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if !r1.Blocked {
		t.Error("expected shell_run to be blocked")
	}

	// should NOT block file_read (matcher mismatch)
	r2 := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "file_read"})
	if r2.Blocked {
		t.Error("expected file_read NOT to be blocked")
	}
}

// ─── Wildcard matcher ─────────────────────────────────────────────────────

func TestHookManager_WildcardMatcher(t *testing.T) {
	dir := t.TempDir()
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Matcher: "*", Command: "echo 'auditing' >&2; exit 2"},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	for _, toolName := range []string{"shell_run", "file_write", "file_read", "search_grep"} {
		r := hm.RunHooks(HookPreToolUse, HookContext{ToolName: toolName})
		if r.Blocked {
			t.Errorf("wildcard inject hook should not block %s", toolName)
		}
		if len(r.Messages) == 0 {
			t.Errorf("expected injected message for %s", toolName)
		}
	}
}

// ─── Block short-circuits remaining hooks ─────────────────────────────────

func TestHookManager_BlockShortCircuits(t *testing.T) {
	dir := t.TempDir()
	// Hook 1: blocks. Hook 2: would inject. Block should short-circuit hook 2.
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: "echo 'STOP' >&2; exit 1"},
				{Command: "echo 'should not run' >&2; exit 2"},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	result := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})

	if !result.Blocked {
		t.Error("expected Blocked=true")
	}
	// The second hook's message should NOT appear because block short-circuited
	if len(result.Messages) > 0 {
		t.Errorf("expected no messages after block, got %v", result.Messages)
	}
}

// ─── Empty config: no hooks ───────────────────────────────────────────────

func TestHookManager_NoConfig_IsEmpty(t *testing.T) {
	dir := t.TempDir()
	hm := newManagerFromDir(t, dir)

	if !hm.IsEmpty() {
		t.Error("expected IsEmpty() = true when no config loaded")
	}

	// Running hooks on empty manager should always return zero-value result
	result := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if result.Blocked || len(result.Messages) > 0 {
		t.Errorf("empty manager should produce empty HookResult, got %+v", result)
	}
}

// ─── GetSources ───────────────────────────────────────────────────────────

func TestHookManager_GetSources(t *testing.T) {
	dir := t.TempDir()
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"SessionStart": {{Command: "exit 0"}},
		},
	})
	hm := newManagerFromDir(t, dir)

	sources := hm.GetSources()
	if len(sources) == 0 {
		t.Error("expected at least one source to be recorded")
	}
	found := false
	for _, s := range sources {
		if strings.HasSuffix(s, "hooks.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hooks.json in sources, got %v", sources)
	}
}

// ─── Environment variables passed to hook ─────────────────────────────────

func TestHookManager_EnvVarsPassedToHook(t *testing.T) {
	dir := t.TempDir()
	// Write the hook tool name to a temp file so we can assert it
	outFile := filepath.Join(dir, "hook_output.txt")
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: "echo \"event=$HOOK_EVENT tool=$HOOK_TOOL_NAME\" > " + outFile},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	hm.RunHooks(HookPreToolUse, HookContext{ToolName: "file_read"})

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("hook did not write output file: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if !strings.Contains(content, "event=PreToolUse") {
		t.Errorf("expected HOOK_EVENT=PreToolUse in output, got: %q", content)
	}
	if !strings.Contains(content, "tool=file_read") {
		t.Errorf("expected HOOK_TOOL_NAME=file_read in output, got: %q", content)
	}
}

// ─── Reload ───────────────────────────────────────────────────────────────

func TestHookManager_Reload(t *testing.T) {
	dir := t.TempDir()
	hm := newManagerFromDir(t, dir)

	if !hm.IsEmpty() {
		t.Error("expected empty before config written")
	}

	// Write config then reload
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {{Command: "exit 0"}},
		},
	})
	hm.Reload()

	if hm.IsEmpty() {
		t.Error("expected non-empty after reload with config")
	}
}
