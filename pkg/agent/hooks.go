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
	// Async controls whether the hook runs in the background.
	Async bool `json:"async,omitempty"`
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
	// Blocked is true if any hook exited with code 1/2 or returned 'deny' JSON.
	Blocked bool
	// BlockReason is the stderr content or JSON reason from the blocking hook.
	BlockReason string
	// Messages collects stderr content from exit-2 hooks or JSON messages.
	Messages []string
	// UpdatedInput contains rewritten tool arguments if returned by a hook.
	UpdatedInput any
	// AdditionalContext contains injected conversation context if returned by a hook.
	AdditionalContext string
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

// mergePluginHooks appends hook definitions from plugin manifests.
func (hm *HookManager) mergePluginHooks(hooks map[string][]HookDef) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	for event, defs := range hooks {
		hm.hooks[event] = append(hm.hooks[event], defs...)
	}
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

		if def.Async {
			go func(d HookDef, c HookContext) {
				_ = hm.runOne(event, d, c)
			}(def, ctx)
			continue
		}

		hr := hm.runOne(event, def, ctx)
		if hr.Blocked {
			// First block wins — short-circuit remaining hooks
			return hr
		}
		result.Messages = append(result.Messages, hr.Messages...)
		if hr.UpdatedInput != nil {
			result.UpdatedInput = hr.UpdatedInput
		}
		if hr.AdditionalContext != "" {
			if result.AdditionalContext != "" {
				result.AdditionalContext += "\n" + hr.AdditionalContext
			} else {
				result.AdditionalContext = hr.AdditionalContext
			}
		}
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

	// Populate Stdin JSON payload
	var stdinBuf bytes.Buffer
	stdinMap := map[string]any{
		"tool_name":  ctx.ToolName,
		"tool_input": ctx.ToolInput,
		"session_id": ctx.SessionID,
	}
	if ctx.Prompt != "" {
		stdinMap["prompt"] = ctx.Prompt
	}
	if ctx.ToolOutput != "" {
		stdinMap["tool_output"] = ctx.ToolOutput
	}
	if ctx.ToolError != "" {
		stdinMap["tool_error"] = ctx.ToolError
	}
	_ = json.NewEncoder(&stdinBuf).Encode(stdinMap)
	cmd.Stdin = &stdinBuf

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

	// Try parsing stdout JSON if present
	var stdoutJSON struct {
		Decision           string         `json:"decision,omitempty"`
		Reason             string         `json:"reason,omitempty"`
		Message            string         `json:"message,omitempty"`
		Modifications      map[string]any `json:"modifications,omitempty"`
		HookSpecificOutput *struct {
			HookEventName            string         `json:"hookEventName,omitempty"`
			PermissionDecision       string         `json:"permissionDecision,omitempty"`
			PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
			UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
			AdditionalContext        string         `json:"additionalContext,omitempty"`
		} `json:"hookSpecificOutput,omitempty"`
	}

	stdoutBytes := stdout.Bytes()
	isJSON := false
	if len(stdoutBytes) > 0 {
		if unmarshalErr := json.Unmarshal(stdoutBytes, &stdoutJSON); unmarshalErr == nil {
			isJSON = true
		}
	}

	// ── Hook outcome evaluation ──
	blocked := false
	blockReason := ""
	var messages []string
	var updatedInput any
	var additionalContext string

	if isJSON {
		// Evaluated via JSON protocol
		decision := stdoutJSON.Decision
		reason := stdoutJSON.Reason
		if stdoutJSON.HookSpecificOutput != nil {
			if stdoutJSON.HookSpecificOutput.PermissionDecision != "" {
				decision = stdoutJSON.HookSpecificOutput.PermissionDecision
			}
			if stdoutJSON.HookSpecificOutput.PermissionDecisionReason != "" {
				reason = stdoutJSON.HookSpecificOutput.PermissionDecisionReason
			}
		}

		if decision == "deny" {
			blocked = true
			blockReason = reason
			if blockReason == "" {
				blockReason = "blocked by hook decision (deny)"
			}
		} else if decision == "allow" {
			blocked = false
		} else {
			// Fallback to exit codes under JSON protocol
			// Exit code 2 is standard block in Claude Code
			if exitCode == 2 {
				blocked = true
				blockReason = reason
				if blockReason == "" {
					blockReason = strings.TrimSpace(stderr.String())
				}
				if blockReason == "" {
					blockReason = "blocked by hook exit code 2"
				}
			} else if exitCode == 1 || exitCode == 3 {
				// Exit code 1/3 is non-blocking error/warning
				blocked = false
				msg := strings.TrimSpace(stderr.String())
				if msg != "" {
					messages = []string{msg}
				}
			}
		}

		// Extracted parameters overrides
		if stdoutJSON.HookSpecificOutput != nil && stdoutJSON.HookSpecificOutput.UpdatedInput != nil {
			updatedInput = stdoutJSON.HookSpecificOutput.UpdatedInput
		} else if stdoutJSON.Modifications != nil {
			if toolInput, ok := stdoutJSON.Modifications["tool_input"]; ok {
				updatedInput = toolInput
			}
		}

		// Extracted context injection
		if stdoutJSON.HookSpecificOutput != nil && stdoutJSON.HookSpecificOutput.AdditionalContext != "" {
			additionalContext = stdoutJSON.HookSpecificOutput.AdditionalContext
		}

		// Extracted prompt/message
		if stdoutJSON.Message != "" {
			messages = append(messages, stdoutJSON.Message)
		}

	} else {
		// Evaluated via legacy exit code protocol (exit 1 = block, exit 2 = inject)
		switch exitCode {
		case 0:
			// continue silently
		case 1:
			blocked = true
			blockReason = strings.TrimSpace(stderr.String())
			if blockReason == "" {
				blockReason = "blocked by hook (exit 1)"
			}
		case 2:
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				messages = []string{msg}
			}
		default:
			// Treat unknown exit code as blocking error
			blocked = true
			blockReason = fmt.Sprintf("hook exited with unexpected code %d: %s", exitCode, strings.TrimSpace(stderr.String()))
		}
	}

	if blocked {
		LogAudit(CatSecurity, "hook_execute_blocked", fmt.Sprintf("Hook blocked operation: %s", blockReason), map[string]any{
			"event":       event,
			"command":     def.Command,
			"tool":        ctx.ToolName,
			"reason":      blockReason,
			"duration_ms": durationMS,
		})
		return HookResult{Blocked: true, BlockReason: blockReason}
	}

	if len(messages) > 0 {
		LogInfo(CatSession, "hook_execute_injected", "Hook injected messages/warnings", map[string]any{
			"event":       event,
			"command":     def.Command,
			"tool":        ctx.ToolName,
			"messages":    messages,
			"duration_ms": durationMS,
		})
	} else {
		LogInfo(CatSession, "hook_execute_success", "Hook executed successfully", map[string]any{
			"event":       event,
			"command":     def.Command,
			"tool":        ctx.ToolName,
			"duration_ms": durationMS,
		})
	}

	return HookResult{
		Messages:          messages,
		UpdatedInput:      updatedInput,
		AdditionalContext: additionalContext,
	}
}

// hookTruncate truncates a string to maxLen bytes (UTF-8 safe enough for env vars).
func hookTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
