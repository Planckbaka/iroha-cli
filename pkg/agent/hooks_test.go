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

// ─── JSON Stdin Protocol ──────────────────────────────────────────────────

func TestHookManager_JSONStdin(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "stdin_out.json")
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: "cat > " + outFile},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	hm.RunHooks(HookPreToolUse, HookContext{
		ToolName:  "shell_run",
		ToolInput: map[string]any{"command": "echo hello"},
		SessionID: "session-123",
	})

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read stdin output: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse stdin output JSON: %v", err)
	}

	if parsed["tool_name"] != "shell_run" {
		t.Errorf("expected tool_name = 'shell_run', got %v", parsed["tool_name"])
	}
	if parsed["session_id"] != "session-123" {
		t.Errorf("expected session_id = 'session-123', got %v", parsed["session_id"])
	}
	toolInput, ok := parsed["tool_input"].(map[string]any)
	if !ok || toolInput["command"] != "echo hello" {
		t.Errorf("expected tool_input command 'echo hello', got %v", parsed["tool_input"])
	}
}

// ─── JSON Stdout: Allow / Deny Decisions ──────────────────────────────────

func TestHookManager_JSONStdout_AllowDeny(t *testing.T) {
	dir := t.TempDir()
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: `echo '{"decision":"deny","reason":"security policy violation"}'`},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	r1 := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if !r1.Blocked {
		t.Error("expected blocked due to JSON decision=deny")
	}
	if r1.BlockReason != "security policy violation" {
		t.Errorf("expected block reason 'security policy violation', got %q", r1.BlockReason)
	}

	// Verify hookSpecificOutput deny
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: `echo '{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"blocked via hookSpecificOutput"}}'`},
			},
		},
	})
	hm.Reload()
	r2 := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if !r2.Blocked {
		t.Error("expected blocked due to hookSpecificOutput decision=deny")
	}
	if r2.BlockReason != "blocked via hookSpecificOutput" {
		t.Errorf("expected block reason 'blocked via hookSpecificOutput', got %q", r2.BlockReason)
	}
}

// ─── JSON Stdout: Modifications & AdditionalContext ─────────────────────────

func TestHookManager_JSONStdout_Modifications(t *testing.T) {
	dir := t.TempDir()
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: `echo '{"hookSpecificOutput":{"permissionDecision":"allow","updatedInput":{"command":"npm test -- --coverage"},"additionalContext":"timeout is 5s"}}'`},
			},
		},
	})
	hm := newManagerFromDir(t, dir)

	r := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if r.Blocked {
		t.Error("should not be blocked")
	}
	if r.AdditionalContext != "timeout is 5s" {
		t.Errorf("expected additional context 'timeout is 5s', got %q", r.AdditionalContext)
	}
	updatedMap, ok := r.UpdatedInput.(map[string]any)
	if !ok || updatedMap["command"] != "npm test -- --coverage" {
		t.Errorf("expected updated input command 'npm test -- --coverage', got %v", r.UpdatedInput)
	}
}

// ─── JSON Stdout: Exit Code Compatibility ─────────────────────────────────

func TestHookManager_ExitCodeCompat(t *testing.T) {
	dir := t.TempDir()
	// Legacy: no JSON, exit 1 blocks, exit 2 injects
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: "exit 1"},
			},
		},
	})
	hm := newManagerFromDir(t, dir)
	r1 := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if !r1.Blocked {
		t.Error("legacy exit 1 should block")
	}

	// Legacy exit 2 should inject (non-blocking)
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: "echo 'warning msg' >&2; exit 2"},
			},
		},
	})
	hm.Reload()
	r2 := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if r2.Blocked {
		t.Error("legacy exit 2 should not block")
	}
	if len(r2.Messages) == 0 || r2.Messages[0] != "warning msg" {
		t.Errorf("expected injected message 'warning msg', got %v", r2.Messages)
	}

	// Official spec with JSON: exit 2 blocks
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: `echo '{"message":"some msg"}'; exit 2`},
			},
		},
	})
	hm.Reload()
	r3 := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if !r3.Blocked {
		t.Error("official spec JSON with exit code 2 should block")
	}

	// Official spec with JSON: exit 1 is non-blocking warning
	writeHooksConfig(t, dir, HookConfig{
		Hooks: map[string][]HookDef{
			"PreToolUse": {
				{Command: `echo '{"message":"json msg"}'; echo 'warning standard' >&2; exit 1`},
			},
		},
	})
	hm.Reload()
	r4 := hm.RunHooks(HookPreToolUse, HookContext{ToolName: "shell_run"})
	if r4.Blocked {
		t.Error("official spec JSON with exit code 1 should NOT block")
	}
	allMsgs := strings.Join(r4.Messages, " ")
	if len(r4.Messages) == 0 || !strings.Contains(allMsgs, "json msg") {
		t.Errorf("expected messages to contain 'json msg', got %v", r4.Messages)
	}
}

