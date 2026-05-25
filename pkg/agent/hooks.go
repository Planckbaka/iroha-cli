package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HookEvent identifies a lifecycle moment in the agent loop.
type HookEvent string

const (
	// HookSessionStart fires once when the runner is created.
	HookSessionStart HookEvent = "SessionStart"
	// HookSessionEnd fires once when the runner shuts down.
	HookSessionEnd HookEvent = "SessionEnd"
	// HookUserPrompt fires before a user prompt is sent to the LLM.
	HookUserPrompt HookEvent = "UserPrompt"
	// HookAgentResponse fires after an LLM response is received.
	HookAgentResponse HookEvent = "AgentResponse"
	// HookPreToolUse fires before every tool call. Can block execution (exit 1).
	HookPreToolUse HookEvent = "PreToolUse"
	// HookPostToolUse fires after every tool call. Can annotate the result (exit 2).
	HookPostToolUse HookEvent = "PostToolUse"
	// HookToolError fires when a tool call returns an error.
	HookToolError HookEvent = "ToolError"
	// HookCompaction fires when context compaction occurs.
	HookCompaction HookEvent = "Compaction"
)

// HookDef is a single hook entry from the config file.
type HookDef struct {
	// Matcher is an optional tool name filter. Empty string or "*" matches all tools.
	Matcher string `json:"matcher,omitempty"`
	// Command is the shell command to execute.
	Command string `json:"command"`
	// Timeout is an optional per-hook timeout in seconds. If > 0, overrides the
	// event-category default timeout for this specific hook.
	Timeout int `json:"timeout,omitempty"`
	// OnTimeout controls behavior when the hook times out.
	// "proceed" (default) means fail-open: log and continue.
	// "block" means fail-closed: treat timeout as a block.
	OnTimeout string `json:"on_timeout,omitempty"`
}

// HookConfig mirrors the structure of a hooks.json config file.
type HookConfig struct {
	Hooks   map[string][]HookDef `json:"hooks"`
	Timeout int                  `json:"timeout,omitempty"`
}

// HookContext carries per-call context passed to hooks via environment variables.
type HookContext struct {
	// ToolName is the name of the tool being called.
	ToolName string
	// ToolInput is the raw arguments passed to the tool.
	ToolInput any
	// ToolOutput is the string output of the tool (only set for PostToolUse).
	ToolOutput string
	// Prompt is the user's prompt text (for HookUserPrompt).
	Prompt string
	// ResponseLength is the length of the agent's response in bytes (for HookAgentResponse).
	ResponseLength int
	// ToolError is the error message from a failed tool call (for HookToolError).
	ToolError string
	// CompactionType describes the compaction phase (for HookCompaction).
	CompactionType string
	// SessionID is the session identifier (for HookSessionEnd).
	SessionID string
}

// HookResult carries the aggregate outcome of running all hooks for an event.
type HookResult struct {
	// Blocked is true if any hook exited with code 1.
	Blocked bool
	// BlockReason is the stderr content from the blocking hook.
	BlockReason string
	// Messages collects stderr content from exit-2 (inject) hooks.
	Messages []string
}

// HookManager loads hook definitions from external config files and executes
// them as subprocesses at lifecycle events in the agent loop.
//
// Config is loaded from two layers (merged in order):
//
//	~/.iroha/hooks.json  (global)
//	./.iroha/hooks.json  (project, takes priority)
//
// The exit-code protocol mirrors the s08 spec:
//
//	0 = continue silently
//	1 = block — tool is NOT executed; stderr returned as error message
//	2 = inject — stderr is injected into the conversation; tool still runs
type HookManager struct {
	mu      sync.RWMutex
	hooks   map[string][]HookDef
	timeout time.Duration
	sources []string // config paths that were successfully loaded
}

// GlobalHookManager is the singleton used by the runner.
var GlobalHookManager = NewHookManager()

// NewHookManager creates a HookManager and loads config from disk.
func NewHookManager() *HookManager {
	hm := &HookManager{
		hooks:   make(map[string][]HookDef),
		timeout: 5 * time.Second,
	}
	hm.load()
	return hm
}

// Reload discards all hooks and re-reads config files from disk.
// Safe to call at runtime (e.g. after /hooks reload).
func (hm *HookManager) Reload() {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.hooks = make(map[string][]HookDef)
	hm.sources = nil
	hm.load()
}

func (hm *HookManager) load() {
	// Layer 1: global config
	if home, err := os.UserHomeDir(); err == nil {
		globalIrohaPath := filepath.Join(home, ".iroha", "hooks.json")
		globalGoClaudePath := filepath.Join(home, ".go-claude", "hooks.json")
		if _, err := os.Stat(globalIrohaPath); os.IsNotExist(err) {
			if _, oldErr := os.Stat(globalGoClaudePath); oldErr == nil {
				_ = os.MkdirAll(filepath.Dir(globalIrohaPath), 0755)
				if data, copyErr := os.ReadFile(globalGoClaudePath); copyErr == nil {
					_ = os.WriteFile(globalIrohaPath, data, 0644)
					_ = os.Rename(globalGoClaudePath, globalGoClaudePath+".bak")
				}
			}
		}
		hm.loadFile(globalIrohaPath)
	}
	// Layer 2: project config (merged on top, same-event hooks are appended)
	if cwd, err := os.Getwd(); err == nil {
		projectIrohaPath := filepath.Join(cwd, ".iroha", "hooks.json")
		projectGoClaudePath := filepath.Join(cwd, ".go-claude", "hooks.json")
		if _, err := os.Stat(projectIrohaPath); os.IsNotExist(err) {
			if _, oldErr := os.Stat(projectGoClaudePath); oldErr == nil {
				_ = os.MkdirAll(filepath.Dir(projectIrohaPath), 0755)
				if data, copyErr := os.ReadFile(projectGoClaudePath); copyErr == nil {
					_ = os.WriteFile(projectIrohaPath, data, 0644)
					_ = os.Rename(projectGoClaudePath, projectGoClaudePath+".bak")
				}
			}
		}
		hm.loadFile(projectIrohaPath)
	}
}

func (hm *HookManager) loadFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // file absent — silently skip
	}
	var cfg HookConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		LogError(CatSession, "hook_parse_failed", fmt.Sprintf("Failed to parse hooks config file: %s", path), err, map[string]any{"path": path})
		return // bad JSON — silently skip
	}
	if cfg.Timeout > 0 {
		hm.timeout = time.Duration(cfg.Timeout) * time.Second
	}
	loadedCount := 0
	for event, defs := range cfg.Hooks {
		hm.hooks[event] = append(hm.hooks[event], defs...)
		loadedCount += len(defs)
	}
	hm.sources = append(hm.sources, path)
	LogInfo(CatSession, "hook_load_success", fmt.Sprintf("Successfully loaded %d hooks from %s", loadedCount, path), map[string]any{
		"path":         path,
		"loaded_count": loadedCount,
	})
}

// GetSources returns the config paths that were successfully loaded.
func (hm *HookManager) GetSources() []string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	out := make([]string, len(hm.sources))
	copy(out, hm.sources)
	return out
}

// GetHooks returns a deep copy of all registered hook definitions.
func (hm *HookManager) GetHooks() map[string][]HookDef {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	snap := make(map[string][]HookDef, len(hm.hooks))
	for k, v := range hm.hooks {
		snap[k] = append([]HookDef{}, v...)
	}
	return snap
}

// IsEmpty returns true if no hooks are registered for any event.
func (hm *HookManager) IsEmpty() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	for _, defs := range hm.hooks {
		if len(defs) > 0 {
			return false
		}
	}
	return true
}

// RunHooks executes all hooks registered for the given event and returns
// the aggregated result. The first block (exit 1) short-circuits the chain.
func (hm *HookManager) RunHooks(event HookEvent, ctx HookContext) HookResult {
	hm.mu.RLock()
	defs := append([]HookDef{}, hm.hooks[string(event)]...)
	hm.mu.RUnlock()

	result := HookResult{}
	for _, def := range defs {
		// Apply optional tool-name matcher
		if def.Matcher != "" && def.Matcher != "*" && def.Matcher != ctx.ToolName {
			continue
		}
		if def.Command == "" {
			continue
		}

		hr := hm.runOne(event, def, ctx)
		if hr.Blocked {
			// First block wins — short-circuit remaining hooks
			return hr
		}
		result.Messages = append(result.Messages, hr.Messages...)
	}
	return result
}

// hookTimeoutForEvent returns the default timeout for a given event category.
func hookTimeoutForEvent(event HookEvent) time.Duration {
	switch event {
	case HookPreToolUse, HookPostToolUse, HookToolError:
		return 5 * time.Second
	case HookSessionStart, HookSessionEnd:
		return 10 * time.Second
	case HookUserPrompt, HookAgentResponse:
		return 15 * time.Second
	case HookCompaction:
		return 10 * time.Second
	default:
		return 5 * time.Second
	}
}

// runOne executes a single hook command and interprets its exit code.
func (hm *HookManager) runOne(event HookEvent, def HookDef, ctx HookContext) HookResult {
	start := time.Now()

	// Build environment: standard env + hook context vars
	extraEnv := []string{
		"HOOK_EVENT=" + string(event),
	}
	if ctx.ToolName != "" {
		extraEnv = append(extraEnv, "HOOK_TOOL_NAME="+ctx.ToolName)
	}
	if ctx.ToolInput != nil {
		inputJSON, _ := json.Marshal(ctx.ToolInput)
		extraEnv = append(extraEnv, "HOOK_TOOL_INPUT="+hookTruncate(string(inputJSON), 10000))
	}
	if ctx.ToolOutput != "" {
		extraEnv = append(extraEnv, "HOOK_TOOL_OUTPUT="+hookTruncate(ctx.ToolOutput, 10000))
	}
	if ctx.ToolError != "" {
		extraEnv = append(extraEnv, "HOOK_TOOL_ERROR="+ctx.ToolError)
	}
	if ctx.Prompt != "" {
		extraEnv = append(extraEnv, "HOOK_PROMPT="+hookTruncate(ctx.Prompt, 10000))
	}
	if ctx.ResponseLength > 0 {
		extraEnv = append(extraEnv, "HOOK_RESPONSE_LENGTH="+strconv.Itoa(ctx.ResponseLength))
	}
	if ctx.CompactionType != "" {
		extraEnv = append(extraEnv, "HOOK_COMPACTION_TYPE="+ctx.CompactionType)
	}
	if ctx.SessionID != "" {
		extraEnv = append(extraEnv, "HOOK_SESSION_ID="+ctx.SessionID)
	}

	// Determine timeout: per-hook override > event-category default
	timeout := hookTimeoutForEvent(event)
	if def.Timeout > 0 {
		timeout = time.Duration(def.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", def.Command)
	cmd.Env = append(os.Environ(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	durationMS := time.Since(start).Milliseconds()

	// Determine exit code
	exitCode := 0
	if err != nil {
		if execCtx.Err() != nil {
			// Timeout behavior depends on def.OnTimeout
			fmt.Fprintf(os.Stderr, "[hook:%s] timeout (%v)\n", event, timeout)
			LogError(CatSession, "hook_timeout", fmt.Sprintf("Hook command timed out after %v", timeout), execCtx.Err(), map[string]any{
				"event":       event,
				"command":     def.Command,
				"tool":        ctx.ToolName,
				"duration_ms": durationMS,
			})
			if def.OnTimeout == "block" {
				return HookResult{Blocked: true, BlockReason: fmt.Sprintf("hook timed out after %v", timeout)}
			}
			// Default: fail-open — log and continue
			return HookResult{}
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Unexpected crash (not an ExitError) — always fail-closed
			LogError(CatSession, "hook_crash", "Hook command crashed unexpectedly", err, map[string]any{
				"event":       event,
				"command":     def.Command,
				"tool":        ctx.ToolName,
				"duration_ms": durationMS,
			})
			return HookResult{Blocked: true, BlockReason: fmt.Sprintf("hook crashed: %v", err)}
		}
	}

	switch exitCode {
	case 0:
		LogInfo(CatSession, "hook_execute_success", "Hook executed successfully with exit code 0", map[string]any{
			"event":       event,
			"command":     def.Command,
			"tool":        ctx.ToolName,
			"duration_ms": durationMS,
		})
		return HookResult{}

	case 1:
		// Block: stderr is the reason message
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = "blocked by hook (no message)"
		}
		LogAudit(CatSecurity, "hook_execute_blocked", fmt.Sprintf("Hook blocked operation: %s", reason), map[string]any{
			"event":       event,
			"command":     def.Command,
			"tool":        ctx.ToolName,
			"reason":      reason,
			"duration_ms": durationMS,
		})
		return HookResult{Blocked: true, BlockReason: reason}

	case 2:
		// Inject: stderr is appended as a message
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			LogInfo(CatSession, "hook_execute_injected", "Hook injected message into conversation", map[string]any{
				"event":       event,
				"command":     def.Command,
				"tool":        ctx.ToolName,
				"message":     msg,
				"duration_ms": durationMS,
			})
			return HookResult{Messages: []string{msg}}
		}
		return HookResult{}

	default:
		// Unknown exit code — always fail-closed (unexpected crash)
		LogWarn(CatSession, "hook_execute_unknown_code", fmt.Sprintf("Hook executed with unexpected exit code: %d", exitCode), map[string]any{
			"event":       event,
			"command":     def.Command,
			"tool":        ctx.ToolName,
			"exit_code":   exitCode,
			"duration_ms": durationMS,
			"stderr":      stderr.String(),
			"stdout":      stdout.String(),
		})
		return HookResult{Blocked: true, BlockReason: fmt.Sprintf("hook exited with unexpected code %d: %s", exitCode, strings.TrimSpace(stderr.String()))}
	}
}

// hookTruncate truncates a string to maxLen bytes (UTF-8 safe enough for env vars).
func hookTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
