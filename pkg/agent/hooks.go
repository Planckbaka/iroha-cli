package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HookEvent identifies a lifecycle moment in the agent loop.
type HookEvent string

const (
	// HookSessionStart fires once when the runner is created.
	HookSessionStart HookEvent = "SessionStart"
	// HookPreToolUse fires before every tool call. Can block execution (exit 1).
	HookPreToolUse HookEvent = "PreToolUse"
	// HookPostToolUse fires after every tool call. Can annotate the result (exit 2).
	HookPostToolUse HookEvent = "PostToolUse"
)

// HookDef is a single hook entry from the config file.
type HookDef struct {
	// Matcher is an optional tool name filter. Empty string or "*" matches all tools.
	Matcher string `json:"matcher,omitempty"`
	// Command is the shell command to execute.
	Command string `json:"command"`
}

// HookConfig mirrors the structure of a hooks.json config file.
type HookConfig struct {
	Hooks map[string][]HookDef `json:"hooks"`
}

// HookContext carries per-call context passed to hooks via environment variables.
type HookContext struct {
	// ToolName is the name of the tool being called.
	ToolName string
	// ToolInput is the raw arguments passed to the tool.
	ToolInput any
	// ToolOutput is the string output of the tool (only set for PostToolUse).
	ToolOutput string
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
		timeout: 30 * time.Second,
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
		return // bad JSON — silently skip
	}
	for event, defs := range cfg.Hooks {
		hm.hooks[event] = append(hm.hooks[event], defs...)
	}
	hm.sources = append(hm.sources, path)
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

// runOne executes a single hook command and interprets its exit code.
func (hm *HookManager) runOne(event HookEvent, def HookDef, ctx HookContext) HookResult {
	// Build environment: standard env + hook context vars
	inputJSON, _ := json.Marshal(ctx.ToolInput)
	extraEnv := []string{
		"HOOK_EVENT=" + string(event),
		"HOOK_TOOL_NAME=" + ctx.ToolName,
		"HOOK_TOOL_INPUT=" + hookTruncate(string(inputJSON), 10000),
	}
	if event == HookPostToolUse && ctx.ToolOutput != "" {
		extraEnv = append(extraEnv, "HOOK_TOOL_OUTPUT="+hookTruncate(ctx.ToolOutput, 10000))
	}

	execCtx, cancel := context.WithTimeout(context.Background(), hm.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", def.Command)
	cmd.Env = append(os.Environ(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Determine exit code
	exitCode := 0
	if err != nil {
		if execCtx.Err() != nil {
			// Timeout — treat as continue (log only)
			fmt.Fprintf(os.Stderr, "[hook:%s] timeout (%v)\n", event, hm.timeout)
			return HookResult{}
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	switch exitCode {
	case 0:
		// Continue silently
		return HookResult{}

	case 1:
		// Block: stderr is the reason message
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = "blocked by hook (no message)"
		}
		return HookResult{Blocked: true, BlockReason: reason}

	case 2:
		// Inject: stderr is appended as a message
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return HookResult{Messages: []string{msg}}
		}
		return HookResult{}

	default:
		// Unknown exit code — treat as continue
		return HookResult{}
	}
}

// hookTruncate truncates a string to maxLen bytes (UTF-8 safe enough for env vars).
func hookTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
