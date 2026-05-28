package agent

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
	AllowedEnvVars []string          `json:"allowedEnvVars,omitempty"`
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
