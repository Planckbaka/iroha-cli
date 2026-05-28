package agent

import (
	"encoding/json"
	"fmt"
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
	// HookSubagentStop fires when a subagent completes execution.
	HookSubagentStop HookEvent = "SubagentStop"
	// HookNotification fires when a background task or cron triggers a notification.
	HookNotification HookEvent = "Notification"
	// HookPreCompact fires before context compaction.
	HookPreCompact HookEvent = "PreCompact"
	// HookPostCompact fires after context compaction.
	HookPostCompact HookEvent = "PostCompact"
)

// HookType represents the execution style of a hook
type HookType string

const (
	HookTypeCommand HookType = "command"
	HookTypeHTTP    HookType = "http"
	HookTypePrompt  HookType = "llm-prompt"
)

// HookDef is a single hook entry from the config file.
type HookDef struct {
	Type           HookType          `json:"type,omitempty"`
	Matcher        string            `json:"matcher,omitempty"`
	Command        string            `json:"command,omitempty"`
	URL            string            `json:"url,omitempty"`
	Prompt         string            `json:"prompt,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	AllowedEnvVars []string          `json:"allowed_env_vars,omitempty"`
	Timeout        int               `json:"timeout,omitempty"`
	OnTimeout      string            `json:"on_timeout,omitempty"`
	Async          bool              `json:"async,omitempty"`
}

// HookConfig mirrors the structure of a hooks.json config file.
type HookConfig struct {
	Hooks   map[string][]HookDef `json:"hooks"`
	Timeout int                  `json:"timeout,omitempty"`
}

// HookContext carries per-call context passed to hooks via environment variables.
type HookContext struct {
	ToolName       string
	ToolInput      any
	ToolOutput     string
	Prompt         string
	ResponseLength int
	ToolError      string
	CompactionType string
	SessionID      string
}

// HookResult carries the aggregate outcome of running all hooks for an event.
type HookResult struct {
	Blocked           bool
	BlockReason       string
	Messages          []string
	UpdatedInput      any
	AdditionalContext string
}

func hookTimeoutForEvent(event HookEvent) time.Duration {
	switch event {
	case HookPreToolUse, HookPostToolUse, HookToolError:
		return 5 * time.Second
	case HookSessionStart, HookSessionEnd, HookSubagentStop:
		return 10 * time.Second
	case HookUserPrompt, HookAgentResponse:
		return 15 * time.Second
	case HookCompaction, HookPreCompact, HookPostCompact:
		return 10 * time.Second
	default:
		return 5 * time.Second
	}
}

func parseJSONResult(event HookEvent, jsonBytes []byte, durationMS int64, ctx HookContext, exitCode int) HookResult {
	var jsonOutput struct {
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

	if err := json.Unmarshal(jsonBytes, &jsonOutput); err != nil {
		// If fails to parse JSON, assume success for compatibility or log warning
		return HookResult{}
	}

	blocked := false
	blockReason := ""
	var messages []string
	var updatedInput any
	var additionalContext string

	decision := jsonOutput.Decision
	reason := jsonOutput.Reason
	if jsonOutput.HookSpecificOutput != nil {
		if jsonOutput.HookSpecificOutput.PermissionDecision != "" {
			decision = jsonOutput.HookSpecificOutput.PermissionDecision
		}
		if jsonOutput.HookSpecificOutput.PermissionDecisionReason != "" {
			reason = jsonOutput.HookSpecificOutput.PermissionDecisionReason
		}
	}

	if decision == "deny" {
		blocked = true
		blockReason = reason
		if blockReason == "" {
			blockReason = "blocked by hook decision (deny)"
		}
	}

	if exitCode == 2 {
		blocked = true
		if blockReason == "" {
			if reason != "" {
				blockReason = reason
			} else {
				blockReason = "blocked by hook exit code 2"
			}
		}
	}

	if jsonOutput.Message != "" {
		messages = append(messages, jsonOutput.Message)
	}

	if jsonOutput.HookSpecificOutput != nil {
		if jsonOutput.HookSpecificOutput.UpdatedInput != nil {
			updatedInput = jsonOutput.HookSpecificOutput.UpdatedInput
		}
		if jsonOutput.HookSpecificOutput.AdditionalContext != "" {
			additionalContext = jsonOutput.HookSpecificOutput.AdditionalContext
		}
	} else if jsonOutput.Modifications != nil {
		if toolInput, ok := jsonOutput.Modifications["tool_input"]; ok {
			updatedInput = toolInput
		}
	}

	if blocked {
		LogAudit(CatSecurity, "hook_execute_blocked", fmt.Sprintf("Hook blocked operation: %s", blockReason), map[string]any{
			"event":       event,
			"reason":      blockReason,
			"duration_ms": durationMS,
		})
		return HookResult{Blocked: true, BlockReason: blockReason}
	}

	return HookResult{
		Messages:          messages,
		UpdatedInput:      updatedInput,
		AdditionalContext: additionalContext,
	}
}

// mergePluginHooks appends hook definitions from plugin manifests.
func (hm *HookManager) mergePluginHooks(hooks map[string][]HookDef) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	for event, defs := range hooks {
		hm.hooks[event] = append(hm.hooks[event], defs...)
	}
}

// hookTruncate truncates a string to maxLen bytes (UTF-8 safe enough for env vars).
func hookTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
