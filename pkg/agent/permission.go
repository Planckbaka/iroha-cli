package agent

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

type PermissionMode string

const (
	ModeDefault     PermissionMode = "default"
	ModePlan        PermissionMode = "plan"
	ModeAuto        PermissionMode = "auto"
	ModeAcceptEdits PermissionMode = "acceptEdits"
	ModeBypass      PermissionMode = "bypassPermissions"
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
			{"heredoc", `<<-?|<<<`, regexp.MustCompile(`<<-?|<<<`)},
			{"process_substitution", `[<>]\(`, regexp.MustCompile(`[<>]\(`)},
			{"named_pipe", `\b(mkfifo|mknod)\b`, regexp.MustCompile(`\b(mkfifo|mknod)\b`)},
			{"terminal_escape", `\\x1[bB]|\\033|\\e`, regexp.MustCompile(`\\x1[bB]|\\033|\\e`)},
			{"file_descriptor", `exec\s+\d+>|[<>]&\d`, regexp.MustCompile(`exec\s+\d+>|[<>]&\d`)},
			{"unsafe_source", `(?:^|\s)(?:source|\.)\s+/`, regexp.MustCompile(`(?:^|\s)(?:source|\.)\s+/`)},
			{"encoding_attack", `\\x[0-9a-fA-F]{2}|\\u[0-9a-fA-F]{4}|\\U[0-9a-fA-F]{8}`, regexp.MustCompile(`\\x[0-9a-fA-F]{2}|\\u[0-9a-fA-F]{4}|\\U[0-9a-fA-F]{8}`)},
			{"proxy_injection", `ProxyCommand=|git\s+-c\s+.*=\s*`, regexp.MustCompile(`ProxyCommand=|git\s+-c\s+.*=\s*`)},
			{"unsafe_find_pipe", `find\b.*\|\s*while\s+read\b.*\b(rm|mv)\b`, regexp.MustCompile(`find\b.*\|\s*while\s+read\b.*\b(rm|mv)\b`)},
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
			{Tool: "file_edit", Path: "*", Behavior: "ask"},
			{Tool: "list_directory", Behavior: "allow"},
			{Tool: "search_grep", Behavior: "allow"},
			{Tool: "find_files", Behavior: "allow"},
			{Tool: "todo", Behavior: "allow"},
			{Tool: "task_create", Behavior: "allow"},
			{Tool: "task_update", Behavior: "allow"},
			{Tool: "task_list", Behavior: "allow"},
			{Tool: "task_get", Behavior: "allow"},
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
			{Tool: "mcp__*", Behavior: "ask"},
			// s22: Web Tools
			{Tool: "web_fetch", Behavior: "allow"},
			{Tool: "web_search", Behavior: "allow"},
		},
	}
}

var GlobalPermissionManager = NewPermissionManager(ModeDefault)

func (pm *PermissionManager) SetMode(mode PermissionMode) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if mode != ModeDefault && mode != ModePlan && mode != ModeAuto && mode != ModeAcceptEdits && mode != ModeBypass {
		return fmt.Errorf("invalid mode: %s", mode)
	}
	oldMode := pm.mode
	pm.mode = mode
	LogAudit(CatSecurity, "mode_change", fmt.Sprintf("Permission mode changed from '%s' to '%s'", oldMode, mode), map[string]any{
		"old_mode": oldMode,
		"new_mode": mode,
	})
	return nil
}

func (pm *PermissionManager) GetMode() PermissionMode {
	pm.mu.RLock()
	m := pm.mode
	pm.mu.RUnlock()
	return m
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
	LogAudit(CatSecurity, "rule_add", fmt.Sprintf("Security rule added: %s %s -> %s", rule.Tool, rule.Path, rule.Behavior), map[string]any{
		"rule": rule,
	})
}

func (pm *PermissionManager) Check(toolName string, args any) (string, string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	LogInfo(CatSecurity, "check_start", fmt.Sprintf("Evaluating permissions for tool '%s'", toolName), map[string]any{
		"tool": toolName,
		"args": args,
	})

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
				if strings.Contains(f, "sudo") || strings.Contains(f, "rm_rf") || strings.Contains(f, "unsafe_find_pipe") || strings.Contains(f, "proxy_injection") {
					isSevere = true
				}
			}
			desc := strings.Join(failures, ", ")
			if isSevere {
				pm.consecutiveDenials++
				reason := fmt.Sprintf("Security Gate: dangerous pattern detected: %s", desc)
				LogAudit(CatSecurity, "security_gate_deny", reason, map[string]any{
					"tool":     toolName,
					"command":  cmdStr,
					"failures": failures,
				})
				return "deny", reason
			}
			// Other patterns escalate to ask
			reason := fmt.Sprintf("Security Gate warning: %s", desc)
			LogWarn(CatSecurity, "security_gate_warn", reason, map[string]any{
				"tool":     toolName,
				"command":  cmdStr,
				"failures": failures,
			})
			return "ask", reason
		}
	}

	// Step 1: Deny rules (always checked first)
	for _, rule := range pm.rules {
		if rule.Behavior != "deny" {
			continue
		}
		if pm.matches(rule, toolName, args) {
			pm.consecutiveDenials++
			reason := fmt.Sprintf("Blocked by deny rule: %v", rule)
			LogAudit(CatSecurity, "rule_deny", reason, map[string]any{
				"tool": toolName,
				"args": args,
				"rule": rule,
			})
			return "deny", reason
		}
	}

	// Step 2: Mode-based decisions
	isWrite := (toolName == "file_write" || toolName == "file_edit" || toolName == "shell_run" || toolName == "background_run" || strings.HasPrefix(toolName, "mcp__"))

	if pm.mode == ModePlan && isWrite {
		pm.consecutiveDenials++
		reason := "Blocked by Plan mode: write operations are forbidden"
		LogAudit(CatSecurity, "mode_plan_block", reason, map[string]any{
			"tool": toolName,
			"args": args,
		})
		return "deny", reason
	}

	if pm.mode == ModeBypass {
		pm.consecutiveDenials = 0
		reason := "Auto-approved by bypassPermissions mode"
		LogAudit(CatSecurity, "mode_bypass_allow", reason, map[string]any{
			"tool": toolName,
			"args": args,
		})
		return "allow", reason
	}

	if pm.mode == ModeAcceptEdits {
		isFileEdit := (toolName == "file_write" || toolName == "file_edit" || toolName == "file_delete")
		if isFileEdit {
			pm.consecutiveDenials = 0
			reason := "Auto-approved by acceptEdits mode"
			LogAudit(CatSecurity, "mode_accept_edits_allow", reason, map[string]any{
				"tool": toolName,
				"args": args,
			})
			return "allow", reason
		}
	}

	// Phase 2: Auto mode uses 4-tier risk classifier
	if pm.mode == ModeAuto {
		tier, tierReason := ClassifyTool(toolName, args)
		switch tier {
		case TierTrusted, TierLowRisk:
			// Auto-approve trusted and low-risk operations
			pm.consecutiveDenials = 0
			reason := fmt.Sprintf("Auto mode: %s (%s)", tierReason, tier)
			LogAudit(CatSecurity, "mode_auto_allow", reason, map[string]any{
				"tool": toolName,
				"tier": tier.String(),
				"args": args,
			})
			return "allow", reason
		case TierMediumRisk, TierHighRisk:
			// Medium and high risk: ask human (LLM review can be added later for medium)
			reason := fmt.Sprintf("Auto mode: %s (%s), requires human confirmation", tierReason, tier)
			LogAudit(CatSecurity, "mode_auto_escalate", reason, map[string]any{
				"tool": toolName,
				"tier": tier.String(),
				"args": args,
			})
			return "ask", reason
		default:
			// Fail-safe: unknown tier treated as high risk
			reason := fmt.Sprintf("Auto mode: unknown risk tier for %q, requires human confirmation", toolName)
			LogAudit(CatSecurity, "mode_auto_escalate", reason, map[string]any{
				"tool": toolName,
				"tier": "unknown",
				"args": args,
			})
			return "ask", reason
		}
	}

	// Step 3: Allow rules
	for _, rule := range pm.rules {
		if rule.Behavior != "allow" {
			continue
		}
		if pm.matches(rule, toolName, args) {
			pm.consecutiveDenials = 0
			reason := fmt.Sprintf("Approved by allow rule: %v", rule)
			LogAudit(CatSecurity, "rule_allow", reason, map[string]any{
				"tool": toolName,
				"args": args,
				"rule": rule,
			})
			return "allow", reason
		}
	}

	// Step 4: Ask user
	reason := "No matching rules, requiring user confirmation"
	LogInfo(CatSecurity, "confirmation_escalate", reason, map[string]any{
		"tool": toolName,
		"args": args,
	})
	return "ask", reason
}

func (pm *PermissionManager) NoteApproval() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.consecutiveDenials = 0
	LogAudit(CatSecurity, "human_allow", "Human approved sensitive operation", nil)
}

func (pm *PermissionManager) NoteDenial() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.consecutiveDenials++
	LogAudit(CatSecurity, "human_deny", "Human denied sensitive operation", map[string]any{
		"consecutive_denials": pm.consecutiveDenials,
	})
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
	LogInfo(CatSecurity, "reset_denials", "Consecutive denials count reset to 0", nil)
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
		} else if m, ok := args.(FileEditArgs); ok {
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
	pLower := strings.ToLower(pattern)
	vLower := strings.ToLower(val)

	// If no wildcard is present, do a substring check to maintain full backward compatibility
	if !strings.Contains(pLower, "*") {
		return strings.Contains(vLower, pLower)
	}

	// Dynamic glob matching
	parts := strings.Split(pLower, "*")
	if len(parts) == 1 {
		return vLower == pLower
	}
	if !strings.HasPrefix(vLower, parts[0]) {
		return false
	}
	vLower = vLower[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(vLower, parts[i])
		if idx == -1 {
			return false
		}
		vLower = vLower[idx+len(parts[i]):]
	}
	return strings.HasSuffix(vLower, parts[len(parts)-1])
}
