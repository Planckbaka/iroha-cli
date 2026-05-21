package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

type PermissionMode string

const (
	ModeDefault PermissionMode = "default"
	ModePlan    PermissionMode = "plan"
	ModeAuto    PermissionMode = "auto"
)

type PermissionRule struct {
	Tool     string `json:"tool"`
	Path     string `json:"path,omitempty"`
	Content  string `json:"content,omitempty"`
	Behavior string `json:"behavior"` // "allow", "deny"
}

type BashSecurityValidator struct {
	patterns []struct {
		name    string
		pattern string
		re      *regexp.Regexp
	}
}

func NewBashSecurityValidator() *BashSecurityValidator {
	return &BashSecurityValidator{
		patterns: []struct {
			name    string
			pattern string
			re      *regexp.Regexp
		}{
			{"shell_metachar", `[;&|` + "`" + `$]`, regexp.MustCompile(`[;&|` + "`" + `$]`)},
			{"sudo", `\bsudo\b`, regexp.MustCompile(`\bsudo\b`)},
			{"rm_rf", `\brm\s+(-[a-zA-Z]*)?r`, regexp.MustCompile(`\brm\s+(-[a-zA-Z]*)?r`)},
			{"cmd_substitution", `\$\(`, regexp.MustCompile(`\$\(`)},
			{"ifs_injection", `\bIFS\s*=`, regexp.MustCompile(`\bIFS\s*=`)},
		},
	}
}

func (v *BashSecurityValidator) Validate(command string) []string {
	var failures []string
	for _, p := range v.patterns {
		if p.re.MatchString(command) {
			failures = append(failures, fmt.Sprintf("%s (pattern: %s)", p.name, p.pattern))
		}
	}
	return failures
}

type PermissionManager struct {
	mu                 sync.RWMutex
	mode               PermissionMode
	rules              []PermissionRule
	consecutiveDenials int
	maxDenials         int
	validator          *BashSecurityValidator
}

func NewPermissionManager(mode PermissionMode) *PermissionManager {
	return &PermissionManager{
		mode:       mode,
		maxDenials: 3,
		validator:  NewBashSecurityValidator(),
		rules: []PermissionRule{
			{Tool: "shell_run", Content: "rm -rf /", Behavior: "deny"},
			{Tool: "shell_run", Content: "sudo *", Behavior: "deny"},
			{Tool: "background_run", Content: "rm -rf /", Behavior: "deny"},
			{Tool: "background_run", Content: "sudo *", Behavior: "deny"},
			{Tool: "file_read", Path: "*", Behavior: "allow"},
			{Tool: "list_directory", Behavior: "allow"},
			{Tool: "search_grep", Behavior: "allow"},
			{Tool: "todo", Behavior: "allow"},
			{Tool: "task_create", Behavior: "allow"},
			{Tool: "task_update", Behavior: "allow"},
			{Tool: "task_list", Behavior: "allow"},
			{Tool: "task_get", Behavior: "allow"},
			{Tool: "background_run", Behavior: "allow"},
			{Tool: "check_background", Behavior: "allow"},
			{Tool: "schedule_create", Behavior: "allow"},
			{Tool: "schedule_list", Behavior: "allow"},
			{Tool: "schedule_delete", Behavior: "allow"},
			// s15: Teams
			{Tool: "spawn_teammate", Behavior: "allow"},
			{Tool: "list_teammates", Behavior: "allow"},
			{Tool: "send_message", Behavior: "allow"},
			{Tool: "read_inbox", Behavior: "allow"},
			{Tool: "broadcast", Behavior: "allow"},
			// s16: Protocols
			{Tool: "protocol_shutdown_request", Behavior: "allow"},
			{Tool: "protocol_shutdown_response", Behavior: "allow"},
			{Tool: "protocol_plan_approval_request", Behavior: "allow"},
			{Tool: "protocol_plan_approval_response", Behavior: "allow"},
			// s17: Autonomy
			{Tool: "agent_claim_task", Behavior: "allow"},
			{Tool: "agent_set_state", Behavior: "allow"},
			// s18: Worktrees
			{Tool: "worktree_create", Behavior: "allow"},
			{Tool: "worktree_list", Behavior: "allow"},
			{Tool: "worktree_status", Behavior: "allow"},
			{Tool: "worktree_enter", Behavior: "allow"},
			{Tool: "worktree_closeout", Behavior: "allow"},
			// s19: MCP & Plugin
			{Tool: "mcp_server_list", Behavior: "allow"},
			{Tool: "mcp__*", Behavior: "allow"},
		},
	}
}

var GlobalPermissionManager = NewPermissionManager(ModeDefault)

func (pm *PermissionManager) SetMode(mode PermissionMode) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if mode != ModeDefault && mode != ModePlan && mode != ModeAuto {
		return fmt.Errorf("invalid mode: %s", mode)
	}
	pm.mode = mode
	return nil
}

func (pm *PermissionManager) GetMode() PermissionMode {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.mode
}

func (pm *PermissionManager) GetRules() []PermissionRule {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	rulesCopy := make([]PermissionRule, len(pm.rules))
	copy(rulesCopy, pm.rules)
	return rulesCopy
}

func (pm *PermissionManager) AddRule(rule PermissionRule) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.rules = append(pm.rules, rule)
}

func (pm *PermissionManager) Check(toolName string, args any) (string, string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Step 0: Bash security validation (for bash command writes)
	if toolName == "shell_run" || toolName == "background_run" {
		cmdStr := ""
		if m, ok := args.(map[string]any); ok {
			cmdStr, _ = m["command"].(string)
		} else if m, ok := args.(ShellRunArgs); ok {
			cmdStr = m.Command
		} else if m, ok := args.(BackgroundRunArgs); ok {
			cmdStr = m.Command
		}
		failures := pm.validator.Validate(cmdStr)
		if len(failures) > 0 {
			// Severe patterns (sudo, rm_rf) get immediate deny
			isSevere := false
			for _, f := range failures {
				if strings.Contains(f, "sudo") || strings.Contains(f, "rm_rf") {
					isSevere = true
				}
			}
			desc := strings.Join(failures, ", ")
			if isSevere {
				pm.consecutiveDenials++
				return "deny", fmt.Sprintf("Security Gate: dangerous pattern detected: %s", desc)
			}
			// Other patterns escalate to ask
			return "ask", fmt.Sprintf("Security Gate warning: %s", desc)
		}
	}

	// Step 1: Deny rules (always checked first)
	for _, rule := range pm.rules {
		if rule.Behavior != "deny" {
			continue
		}
		if pm.matches(rule, toolName, args) {
			pm.consecutiveDenials++
			return "deny", fmt.Sprintf("Blocked by deny rule: %v", rule)
		}
	}

	// Step 2: Mode-based decisions
	isWrite := (toolName == "file_write" || toolName == "shell_run")
	isRead := (toolName == "file_read" || toolName == "search_grep" || toolName == "todo")

	if pm.mode == ModePlan && isWrite {
		pm.consecutiveDenials++
		return "deny", "Blocked by Plan mode: write operations are forbidden"
	}

	if pm.mode == ModeAuto && isRead {
		pm.consecutiveDenials = 0
		return "allow", "Auto mode: read operations auto-approved"
	}

	// Step 3: Allow rules
	for _, rule := range pm.rules {
		if rule.Behavior != "allow" {
			continue
		}
		if pm.matches(rule, toolName, args) {
			pm.consecutiveDenials = 0
			return "allow", fmt.Sprintf("Approved by allow rule: %v", rule)
		}
	}

	// Step 4: Ask user
	return "ask", "No matching rules, requiring user confirmation"
}

func (pm *PermissionManager) NoteApproval() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.consecutiveDenials = 0
}

func (pm *PermissionManager) NoteDenial() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.consecutiveDenials++
	return pm.consecutiveDenials
}

func (pm *PermissionManager) ConsecutiveDenials() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.consecutiveDenials
}

func (pm *PermissionManager) ResetConsecutiveDenials() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.consecutiveDenials = 0
}

func (pm *PermissionManager) matches(rule PermissionRule, toolName string, args any) bool {
	// 1. Tool name match
	if rule.Tool != "*" && rule.Tool != "" {
		if !matchesPattern(rule.Tool, toolName) {
			return false
		}
	}

	// 2. Path pattern match (for file tools)
	if rule.Path != "" && rule.Path != "*" {
		pathVal := ""
		if m, ok := args.(map[string]any); ok {
			pathVal, _ = m["path"].(string)
		} else if m, ok := args.(FileReadArgs); ok {
			pathVal = m.Path
		} else if m, ok := args.(FileWriteArgs); ok {
			pathVal = m.Path
		}
		if !matchesPattern(rule.Path, pathVal) {
			return false
		}
	}

	// 3. Content match (for shell command)
	if rule.Content != "" && rule.Content != "*" {
		cmdVal := ""
		if m, ok := args.(map[string]any); ok {
			cmdVal, _ = m["command"].(string)
		} else if m, ok := args.(ShellRunArgs); ok {
			cmdVal = m.Command
		}
		if !matchesPattern(rule.Content, cmdVal) {
			return false
		}
	}

	return true
}

func matchesPattern(pattern, val string) bool {
	if pattern == "*" || pattern == "" {
		return true
	}
	matched, err := filepath.Match(pattern, val)
	if err == nil && matched {
		return true
	}
	return strings.Contains(strings.ToLower(val), strings.ToLower(pattern))
}
