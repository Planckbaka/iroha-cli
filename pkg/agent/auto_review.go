package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"iroha/pkg/llm"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// --- Phase 2: Expanded security check functions ---

var (
	reHeredoc             = regexp.MustCompile(`<<-?|<<<`)
	reEnvExpansion        = regexp.MustCompile(`\$\{?[A-Za-z_][A-Za-z0-9_]*`)
	reProcessSubstitution = regexp.MustCompile(`[<>]\(`)
	reNamedPipe           = regexp.MustCompile(`\b(mkfifo|mknod)\b`)
	reTTVEscape           = regexp.MustCompile(`\\x1[bB]|\\033|\\e`)
	reFileDescriptor      = regexp.MustCompile(`exec\s+\d+>|[<>]&\d`)
	reUnsafeSource        = regexp.MustCompile(`(?:^|\s)(?:source|\.)\s+/`)
	reEncodingAttack      = regexp.MustCompile(`\\x[0-9a-fA-F]{2}|\\u[0-9a-fA-F]{4}|\\U[0-9a-fA-F]{8}`)
	reProxyInjection      = regexp.MustCompile(`ProxyCommand=|git\s+-c\s+.*=\s*`)
	reUnsafeFindPipe      = regexp.MustCompile(`find\b.*\|\s*while\s+read\b.*\b(rm|mv)\b`)
)

func checkHeredoc(cmd string) (safe bool, reason string) {
	if reHeredoc.MatchString(cmd) {
		return false, "heredoc abuse detected"
	}
	return true, ""
}

func checkEnvExpansion(cmd string) (safe bool, reason string) {
	// Only flag env expansion in write-context commands (redirects, tee, write operations)
	writeIndicators := []string{">", ">>", "tee "}
	normalized := normalizeCommand(cmd)
	for _, indicator := range writeIndicators {
		if strings.Contains(normalized, indicator) {
			if reEnvExpansion.MatchString(cmd) {
				return false, "environment variable expansion in command"
			}
		}
	}
	return true, ""
}

func checkProcessSubstitution(cmd string) (safe bool, reason string) {
	if reProcessSubstitution.MatchString(cmd) {
		return false, "process substitution detected"
	}
	return true, ""
}

func checkNamedPipe(cmd string) (safe bool, reason string) {
	if reNamedPipe.MatchString(cmd) {
		return false, "named pipe creation detected"
	}
	return true, ""
}

func checkTTVEscape(cmd string) (safe bool, reason string) {
	if reTTVEscape.MatchString(cmd) {
		return false, "terminal escape sequence detected"
	}
	return true, ""
}

func checkFileDescriptor(cmd string) (safe bool, reason string) {
	if reFileDescriptor.MatchString(cmd) {
		return false, "file descriptor manipulation detected"
	}
	return true, ""
}

func checkUnsafeSource(cmd string) (safe bool, reason string) {
	if reUnsafeSource.MatchString(cmd) {
		return false, "sourcing external script detected"
	}
	return true, ""
}

func checkEncodingAttack(cmd string) (safe bool, reason string) {
	if reEncodingAttack.MatchString(cmd) {
		return false, "encoding escape sequence detected"
	}
	return true, ""
}

func checkProxyInjection(cmd string) (safe bool, reason string) {
	if reProxyInjection.MatchString(cmd) {
		return false, "proxy command injection detected"
	}
	return true, ""
}

func checkUnsafeFindPipe(cmd string) (safe bool, reason string) {
	if reUnsafeFindPipe.MatchString(cmd) {
		return false, "unsafe find piped to destructive command"
	}
	return true, ""
}

// AutoReviewResult represents the LLM's safety judgment for a shell command
type AutoReviewResult struct {
	Safe   bool   // true = safe to auto-approve, false = needs human review
	Reason string // human-readable explanation
}

// --- Phase 2: 4-Tier Risk Classifier ---

// RiskTier represents the risk classification level for a tool operation.
type RiskTier int

const (
	TierTrusted    RiskTier = iota // Auto-approve, no review
	TierLowRisk                    // Auto-approve with logging
	TierMediumRisk                 // Require LLM review
	TierHighRisk                   // Always ask human
)

func (t RiskTier) String() string {
	switch t {
	case TierTrusted:
		return "trusted"
	case TierLowRisk:
		return "low_risk"
	case TierMediumRisk:
		return "medium_risk"
	case TierHighRisk:
		return "high_risk"
	default:
		return "unknown"
	}
}

// trustedShellCommands is the set of commands auto-approved in shell_run.
var trustedShellCommands = map[string]bool{
	"ls": true, "git status": true, "git log": true, "git diff": true, "git branch": true,
	"cat": true, "head": true, "tail": true, "wc": true, "echo": true, "pwd": true, "which": true, "env": true,
	"go build": true, "go test": true, "go vet": true, "go mod tidy": true,
	"grep": true, "find": true, "sort": true, "uniq": true,
}

// highRiskShellCommands is the set of commands that always require human approval.
var highRiskShellCommands = map[string]bool{
	"rm": true, "sudo": true, "chmod": true, "chown": true,
	"dd": true, "mkfs": true, "fdisk": true,
}

// highRiskShellPrefixes matches command prefixes for piped-destructive patterns.
var highRiskShellPrefixes = []string{
	"curl ", "wget ",
}

// ClassifyTool returns a risk tier for a given tool invocation.
// It uses fail-safe defaults: any error or unknown input maps to TierHighRisk.
func ClassifyTool(toolName string, args any) (RiskTier, string) {
	// Fail-safe: extract shell command for shell tools
	cmdStr := ""
	if toolName == "shell_run" || toolName == "background_run" {
		if m, ok := args.(map[string]any); ok {
			cmdStr, _ = m["command"].(string)
		} else if m, ok := args.(ShellRunArgs); ok {
			cmdStr = m.Command
		} else if m, ok := args.(BackgroundRunArgs); ok {
			cmdStr = m.Command
		}
	}

	switch toolName {
	case "file_read", "list_directory", "search_grep", "find_files":
		return TierTrusted, "read-only tool, auto-approved"

	case "todo", "task_create", "task_update", "task_list", "task_get",
		"check_background", "schedule_create", "schedule_list", "schedule_delete",
		// Teams
		"spawn_teammate", "list_teammates", "send_message", "read_inbox", "broadcast",
		// Protocols
		"protocol_shutdown_request", "protocol_shutdown_response",
		"protocol_plan_approval_request", "protocol_plan_approval_response",
		// Autonomy
		"agent_claim_task", "agent_set_state",
		// Worktrees
		"worktree_create", "worktree_list", "worktree_status", "worktree_enter", "worktree_closeout",
		// MCP
		"mcp_server_list":
		return TierTrusted, fmt.Sprintf("tool %q auto-approved", toolName)

	case "file_write", "file_edit":
		// File writes are low risk (path-based heuristics still apply via ReviewFileOperation)
		return TierLowRisk, fmt.Sprintf("file write tool %s, auto-approved with logging", toolName)

	case "shell_run", "background_run":
		return classifyShellCommand(cmdStr)

	default:
		// Fail-safe: unknown tools are high risk
		return TierHighRisk, fmt.Sprintf("unknown tool %q, requires human approval", toolName)
	}
}

// classifyShellCommand classifies a shell command into a risk tier.
func classifyShellCommand(cmd string) (RiskTier, string) {
	if cmd == "" {
		return TierHighRisk, "empty command, requires human approval"
	}

	normalized := normalizeCommand(cmd)

	// Check high-risk commands first
	parts := strings.Fields(normalized)
	if len(parts) > 0 {
		cmdName := parts[0]

		// Direct high-risk command names
		if highRiskShellCommands[cmdName] {
			return TierHighRisk, fmt.Sprintf("high-risk command %q, requires human approval", cmdName)
		}

		// Check for piped destructive patterns like "curl ... | sh"
		for _, prefix := range highRiskShellPrefixes {
			if strings.HasPrefix(normalized, prefix) {
				if strings.Contains(normalized, "| sh") || strings.Contains(normalized, "| bash") {
					return TierHighRisk, fmt.Sprintf("piped destructive pattern: %s | sh/bash", prefix)
				}
			}
		}

		// Check trusted commands (exact match or prefix match)
		if trustedShellCommands[cmdName] {
			return TierTrusted, fmt.Sprintf("trusted command %q, auto-approved", cmdName)
		}

		// Multi-word trusted commands (e.g. "git status", "go build")
		if len(parts) >= 2 {
			twoWord := parts[0] + " " + parts[1]
			if trustedShellCommands[twoWord] {
				return TierTrusted, fmt.Sprintf("trusted command %q, auto-approved", twoWord)
			}
		}
	}

	// If it has shell metacharacters or dangerous patterns, medium risk (LLM review)
	if strings.ContainsAny(normalized, ";|&$<>`") {
		return TierMediumRisk, "shell metacharacters detected, requires LLM review"
	}

	// Unknown command: medium risk (LLM can review)
	return TierMediumRisk, "unknown command, requires LLM review"
}

// autoReviewConfig holds the LLM model for the safety reviewer
type autoReviewConfig struct {
	Model model.LLM
}

// GlobalAutoReviewConfig is set at startup from the LLM provider config
var GlobalAutoReviewConfig *autoReviewConfig

// SetAutoReviewConfig configures the auto-review LLM from runner startup
func SetAutoReviewConfig(m model.LLM) {
	GlobalAutoReviewConfig = &autoReviewConfig{
		Model: m,
	}
}

// autoReviewSystemPrompt is the safety judge system instruction
const autoReviewSystemPrompt = `You are a strict security reviewer responsible for evaluating whether Shell commands are safe.
	Your task is to determine whether a Shell command can be safely executed in the user workspace without manual user approval.
	
	Judgment criteria:
	- SAFE (safe, auto-approve): read-only operations, e.g. ls, pwd, cat, echo, git status, git log, go build, go test, find, grep, head, tail, wc, which, env
	- UNSAFE (dangerous, requires human review): any write, delete, network request, system modification, permission change, etc.
	
	Response format must be strictly JSON:
	{"safe": true, "reason": "Read-only directory listing, no risk"}
	or
	{"safe": false, "reason": "Delete operation, potential data loss"}
	
	Return only JSON, no additional text.`

// ReviewCommand asks the configured LLM whether a shell command is safe,
// but enforces a Hybrid Safety Guard: local heuristic safety rules act as an absolute
// hard filter and known safe check, while the LLM is only called for unknown custom commands.
func ReviewCommand(cmd string) AutoReviewResult {
	// 1. Run local heuristic review first
	heuristicResult := heuristicReview(cmd)

	// A. If the heuristic review detects bypass tricks or dangerous patterns, we HARD REJECT immediately.
	// We check this by seeing if heuristicReview returned Safe: false AND the reason indicates a hard rule block.
	// Specifically, if heuristicResult is UNSAFE, and it is NOT because of "unknown or custom command", then it is a hard block!
	if !heuristicResult.Safe && !strings.Contains(heuristicResult.Reason, "unknown or custom command") {
		return heuristicResult
	}

	// B. If heuristic review already determined it is a PRE-APPROVED safe command (like ls, git status),
	// we auto-approve instantly, bypassing LLM to optimize speed and cost.
	if heuristicResult.Safe {
		return heuristicResult
	}

	// 2. If it is an "unknown or custom" command (neither pre-approved safe nor a hard reject),
	// we call the LLM to perform semantic analysis if configured.
	if GlobalAutoReviewConfig == nil || GlobalAutoReviewConfig.Model == nil {
		return heuristicResult // Fallback to heuristic (which is UNSAFE for unknown commands)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	llmResult, err := callLLMForReview(ctx, GlobalAutoReviewConfig, cmd)
	if err != nil {
		return heuristicResult // Fallback on failure (UNSAFE)
	}

	// 3. Double-check LLM approval against our hard dangerous rules to prevent prompt injection jailbreaks
	if llmResult.Safe {
		if strings.Contains(cmd, "\n") || strings.Contains(cmd, "\r") {
			return AutoReviewResult{
				Safe:   false,
				Reason: "Safety fuse: LLM judged command safe, but local check detected newline or multiline injection risk",
			}
		}

		normalized := normalizeCommand(cmd)
		// Check dangerous patterns
		dangerousPatterns := []string{
			"rm ", "rmdir", "mv ", "cp ", "chmod", "chown",
			"curl", "wget", "nc ", "ssh", "scp", "rsync",
			"sudo", "su ", "kill", "pkill",
			"dd ", "mkfs", "fdisk",
			">", ">>", "tee", "toolexec",
		}
		hasDangerousPattern := false
		for _, pattern := range dangerousPatterns {
			if strings.Contains(normalized, pattern) {
				hasDangerousPattern = true
				break
			}
		}

		isDangerousFind := false
		if strings.HasPrefix(normalized, "find ") || normalized == "find" {
			if strings.Contains(normalized, "-exec") || strings.Contains(normalized, "-ok") || strings.Contains(normalized, "-delete") {
				isDangerousFind = true
			}
		}

		hasBypass := strings.Contains(normalized, "$(") || strings.Contains(normalized, "`") ||
			strings.Contains(normalized, "eval ") || strings.Contains(normalized, "exec ") ||
			strings.ContainsAny(normalized, ";|&$<>`")

		if hasDangerousPattern || isDangerousFind || hasBypass {
			return AutoReviewResult{
				Safe:   false,
				Reason: "Safety fuse: LLM judged command safe, but local check detected hidden dangerous patterns or bypass risk",
			}
		}
	}

	return llmResult
}

// callLLMForReview makes a non-streaming LLM call for safety judgment via model.LLM
func callLLMForReview(ctx context.Context, cfg *autoReviewConfig, cmd string) (AutoReviewResult, error) {
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					{Text: fmt.Sprintf("Please review the following Shell command:\n```\n%s\n```", cmd)},
				},
			},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Role: "system",
				Parts: []*genai.Part{
					{Text: autoReviewSystemPrompt},
				},
			},
		},
	}

	content, err := llm.CollectNonStreaming(ctx, cfg.Model, req)
	if err != nil {
		return AutoReviewResult{}, err
	}

	// Parse LLM JSON output
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result struct {
		Safe   bool   `json:"safe"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return AutoReviewResult{Safe: false, Reason: "AI review response format error, deferring to human confirmation"}, nil
	}

	return AutoReviewResult{Safe: result.Safe, Reason: result.Reason}, nil
}

// ReviewFileOperation evaluates whether a file write/edit operation is safe.
// Uses path-based heuristics first, then LLM semantic review for edge cases.
func ReviewFileOperation(toolName, filePath, content string) AutoReviewResult {
	// 1. Path-based heuristic review
	heuristicResult := fileHeuristicReview(toolName, filePath, content)
	if heuristicResult.Safe {
		return heuristicResult
	}
	if !strings.Contains(heuristicResult.Reason, "needs semantic review") {
		return heuristicResult
	}

	// 2. LLM semantic review for unknown patterns
	if GlobalAutoReviewConfig == nil || GlobalAutoReviewConfig.Model == nil {
		return AutoReviewResult{Safe: false, Reason: "No LLM reviewer configured, deferring to human confirmation"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	llmResult, err := callLLMForFileReview(ctx, GlobalAutoReviewConfig, toolName, filePath, content)
	if err != nil {
		return AutoReviewResult{Safe: false, Reason: fmt.Sprintf("LLM review failed: %v", err)}
	}

	return llmResult
}

func fileHeuristicReview(toolName, filePath, content string) AutoReviewResult {
	// Normalize path
	normalized := strings.TrimSpace(filePath)

	// Block writes outside workspace
	if strings.HasPrefix(normalized, "/etc/") ||
		strings.HasPrefix(normalized, "/usr/") ||
		strings.HasPrefix(normalized, "/var/") ||
		strings.HasPrefix(normalized, "/sys/") ||
		strings.HasPrefix(normalized, "/proc/") ||
		strings.HasPrefix(normalized, "/dev/") {
		return AutoReviewResult{Safe: false, Reason: "System directory write blocked"}
	}

	// Block writes to sensitive files
	base := strings.ToLower(normalized)
	sensitivePatterns := []string{
		".ssh/", ".gnupg/", ".aws/", ".env", "credentials",
		"id_rsa", "id_ed25519", ".pem", ".key",
		"~/.gitconfig", "~/.bashrc", "~/.zshrc", "~/.profile",
	}
	for _, pattern := range sensitivePatterns {
		if strings.Contains(base, pattern) {
			return AutoReviewResult{Safe: false, Reason: fmt.Sprintf("Sensitive path pattern detected: %s", pattern)}
		}
	}

	// Block if content contains potential secrets
	lowerContent := strings.ToLower(content)
	secretIndicators := []string{
		"password =", "password=", "secret_key =", "secret_key=",
		"private_key =", "private_key=", "api_secret =", "api_secret=",
		"-----begin rsa private key-----",
		"-----begin private key-----",
	}
	for _, indicator := range secretIndicators {
		if strings.Contains(lowerContent, indicator) {
			return AutoReviewResult{Safe: false, Reason: "Potential secret/credential in content"}
		}
	}

	// Safe: writing to typical project files
	safeExtensions := []string{
		".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".rb",
		".md", ".txt", ".json", ".yaml", ".yml", ".toml",
		".css", ".html", ".sql", ".sh", ".mod", ".sum",
		".proto", ".graphql", ".vue", ".svelte",
	}
	for _, ext := range safeExtensions {
		if strings.HasSuffix(base, ext) {
			return AutoReviewResult{
				Safe:   true,
				Reason: fmt.Sprintf("Writing to project file with safe extension (%s)", ext),
			}
		}
	}

	// Unknown extension — needs semantic review
	return AutoReviewResult{
		Safe:   false,
		Reason: "Unknown file extension, needs semantic review",
	}
}

func callLLMForFileReview(ctx context.Context, cfg *autoReviewConfig, toolName, filePath, content string) (AutoReviewResult, error) {
	prompt := fmt.Sprintf(`Review this file operation for safety:
Tool: %s
File: %s
Content preview (first 500 chars): %s

Is this file write operation safe? Consider:
- Is the target path a reasonable project file?
- Does the content look like normal code/config?
- Any signs of credential leaking or destructive patterns?`, toolName, filePath, truncateString(content, 500))

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					{Text: prompt},
				},
			},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Role: "system",
				Parts: []*genai.Part{
					{Text: `You are a security reviewer for file write operations. Respond with JSON only: {"safe": true/false, "reason": "explanation"}`},
				},
			},
		},
	}

	content, err := llm.CollectNonStreaming(ctx, cfg.Model, req)
	if err != nil {
		return AutoReviewResult{}, err
	}

	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result struct {
		Safe   bool   `json:"safe"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return AutoReviewResult{Safe: false, Reason: "AI file review response format error"}, nil
	}

	return AutoReviewResult{Safe: result.Safe, Reason: result.Reason}, nil
}

// normalizeCommand normalizes shell commands by stripping quotes, backslashes, converting all whitespaces to standard spaces, and converting to lowercase.
func normalizeCommand(cmd string) string {
	var sb strings.Builder
	inSingle := false
	inDouble := false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		if ch == '\'' && (i == 0 || cmd[i-1] != '\\') {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && (i == 0 || cmd[i-1] != '\\') {
			inDouble = !inDouble
			continue
		}
		if ch == '\\' && !inSingle && !inDouble {
			continue
		}

		// Convert tabs, newlines, carriage returns to standard spaces
		if ch == '\t' || ch == '\n' || ch == '\r' {
			sb.WriteByte(' ')
		} else {
			sb.WriteByte(ch)
		}
	}

	// Collapse multiple spaces into a single space
	normalized := sb.String()
	var finalSb strings.Builder
	lastWasSpace := false
	for i := 0; i < len(normalized); i++ {
		ch := normalized[i]
		if ch == ' ' {
			if !lastWasSpace {
				finalSb.WriteByte(' ')
				lastWasSpace = true
			}
		} else {
			finalSb.WriteByte(ch)
			lastWasSpace = false
		}
	}

	return strings.ToLower(strings.TrimSpace(finalSb.String()))
}

// heuristicReview performs a fast rule-based safety check (no LLM needed)
// Used in simulate mode or when LLM call fails.
func heuristicReview(cmd string) AutoReviewResult {
	// 1. Raw newline and carriage return check to prevent multiline command injection
	if strings.Contains(cmd, "\n") || strings.Contains(cmd, "\r") {
		return AutoReviewResult{
			Safe:   false,
			Reason: "Rule review: Detected newline or multiline instruction, cascade execution risk, auto-run blocked",
		}
	}

	// 2. State-machine Tokenizer Command Chain Decoupling
	subcmds := tokenizeShellCommand(cmd)
	for _, sub := range subcmds {
		subNorm := normalizeCommand(sub)

		if strings.Contains(subNorm, "$(") || strings.Contains(subNorm, "`") {
			return AutoReviewResult{
				Safe:   false,
				Reason: "Rule review: Detected command substitution or subshell nesting, privilege escalation risk",
			}
		}
		if strings.Contains(subNorm, "eval ") || strings.Contains(subNorm, "exec ") {
			return AutoReviewResult{
				Safe:   false,
				Reason: "Rule review: Detected dynamic execution (eval/exec), auto-run blocked",
			}
		}

		if isPathDangerous(sub) {
			return AutoReviewResult{
				Safe:   false,
				Reason: "Security sandbox blocked: Detected directory traversal or out-of-bounds access to sensitive system paths",
			}
		}

		parts := strings.Fields(subNorm)
		if len(parts) > 0 {
			cmdName := parts[0]
			dangerousNames := map[string]bool{
				"rm": true, "rmdir": true, "mv": true, "chmod": true, "chown": true,
				"curl": true, "wget": true, "nc": true, "ssh": true, "scp": true,
				"rsync": true, "sudo": true, "su": true, "kill": true, "pkill": true,
				"dd": true, "mkfs": true, "fdisk": true, "toolexec": true,
			}
			if dangerousNames[cmdName] {
				return AutoReviewResult{
					Safe:   false,
					Reason: fmt.Sprintf("Rule review: Detected dangerous system command `%s`, auto-approve blocked", cmdName),
				}
			}
		}
	}

	normalized := normalizeCommand(cmd)

	// 3. Shell metacharacter and redirection detection (chained commands, pipes, variables)
	if strings.ContainsAny(normalized, ";|&$<>`") {
		return AutoReviewResult{
			Safe:   false,
			Reason: "Rule review: Detected Shell metacharacters or redirection (;|&$<>`), auto-run blocked",
		}
	}

	// 4. Check dangerous patterns using normalized commands
	dangerousPatterns := []string{
		"rm ", "rmdir", "mv ", "cp ", "chmod", "chown",
		"curl", "wget", "nc ", "ssh", "scp", "rsync",
		"sudo", "su ", "kill", "pkill",
		"dd ", "mkfs", "fdisk",
		">", ">>", "tee", "toolexec",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(normalized, pattern) {
			return AutoReviewResult{
				Safe:   false,
				Reason: fmt.Sprintf("Rule review: Command contains dangerous pattern `%s`, requires human confirmation", strings.TrimSpace(pattern)),
			}
		}
	}

	// Find command execution flags check
	if strings.HasPrefix(normalized, "find ") || normalized == "find" {
		if strings.Contains(normalized, "-exec") || strings.Contains(normalized, "-ok") || strings.Contains(normalized, "-delete") {
			return AutoReviewResult{
				Safe:   false,
				Reason: "Rule review: Detected `find` command with dangerous execution or deletion flags (-exec/-ok/-delete), auto-run blocked",
			}
		}
	}

	// 5. Phase 2 expanded security checks
	newChecks := []func(string) (bool, string){
		checkHeredoc,
		checkEnvExpansion,
		checkProcessSubstitution,
		checkNamedPipe,
		checkTTVEscape,
		checkFileDescriptor,
		checkUnsafeSource,
		checkEncodingAttack,
		checkProxyInjection,
		checkUnsafeFindPipe,
	}
	for _, check := range newChecks {
		if safe, reason := check(cmd); !safe {
			return AutoReviewResult{
				Safe:   false,
				Reason: fmt.Sprintf("Rule review: %s", reason),
			}
		}
	}

	// 6. Safe read-only commands
	safeCommands := []string{
		"ls", "pwd", "cat", "echo", "head", "tail", "wc", "which", "env",
		"git status", "git log", "git diff", "git branch", "git remote",
		"go build", "go test", "go vet", "go list", "go env",
		"find", "grep", "rg", "fd", "tree",
		"date", "whoami", "hostname", "uname",
		"system_profiler", "sysctl", "sw_vers", "systeminfo",
		"df", "du", "free", "top", "ps", "lsof",
		"networksetup", "ifconfig", "ping", "traceroute", "nslookup", "dig",
		"defaults read", "xcodebuild -version", "xcode-select -p",
	}

	for _, safe := range safeCommands {
		if normalized == safe {
			return AutoReviewResult{
				Safe:   true,
				Reason: fmt.Sprintf("Rule review: `%s` is a safe read-only command", strings.Fields(normalized)[0]),
			}
		}
		if strings.HasPrefix(normalized, safe+" ") {
			// Special security checks for specific commands
			if safe == "git remote" {
				sub := strings.TrimPrefix(normalized, "git remote ")
				if !strings.HasPrefix(sub, "-v") && !strings.HasPrefix(sub, "show") {
					continue // Reject from auto-approval, fallback to LLM/human
				}
			}
			if safe == "env" {
				continue // env with arguments is not auto-approved
			}

			return AutoReviewResult{
				Safe:   true,
				Reason: fmt.Sprintf("Rule review: `%s` is a safe read-only command", strings.Fields(normalized)[0]),
			}
		}
	}

	// Unknown — ask human
	return AutoReviewResult{
		Safe:   false,
		Reason: "Rule review: unknown or custom command, deferring to human confirmation",
	}
}

// tokenizeShellCommand splits a complex shell command line into multiple independent subcommand lines.
// It correctly skips semicolons and operators inside single/double quotes.
func tokenizeShellCommand(cmd string) []string {
	var subcmds []string
	var current strings.Builder

	inSingle := false
	inDouble := false
	escaped := false

	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			current.WriteRune(r)
			continue
		}

		if r == '\'' && !inDouble {
			inSingle = !inSingle
			current.WriteRune(r)
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			current.WriteRune(r)
			continue
		}

		if inSingle || inDouble {
			current.WriteRune(r)
			continue
		}

		isOperator := false
		opLen := 1

		if r == ';' {
			isOperator = true
		} else if r == '|' {
			isOperator = true
			if i+1 < len(runes) && runes[i+1] == '|' {
				opLen = 2
			}
		} else if r == '&' {
			isOperator = true
			if i+1 < len(runes) && runes[i+1] == '&' {
				opLen = 2
			}
		}

		if isOperator {
			subStr := strings.TrimSpace(current.String())
			if subStr != "" {
				subcmds = append(subcmds, subStr)
			}
			current.Reset()
			i += (opLen - 1)
			continue
		}

		current.WriteRune(r)
	}

	subStr := strings.TrimSpace(current.String())
	if subStr != "" {
		subcmds = append(subcmds, subStr)
	}

	return subcmds
}

// isPathDangerous checks if a path string contains directory escapes or unauthorized sensitive absolute paths.
func isPathDangerous(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	// 1. Directory traversal check
	if strings.Contains(path, "../") || path == ".." || strings.Contains(path, "..\\") {
		return true
	}

	// 2. Sensitive absolute path checks
	if strings.HasPrefix(path, "/") || strings.Contains(path, " /") {
		// Whitelist standard system executable/library directories and temp files
		whitelists := []string{
			"/bin/", "/usr/bin/", "/usr/local/bin/", "/tmp/", "/usr/lib/", "/lib/",
			"/usr/include/", "/etc/resolv.conf", "/usr/share/", "/var/tmp/",
		}

		isWhitelisted := false
		for _, wl := range whitelists {
			if strings.HasPrefix(path, wl) || strings.Contains(path, " "+wl) {
				isWhitelisted = true
				break
			}
		}

		if !isWhitelisted {
			// Check sensitive folders
			sensitivePrefixes := []string{
				"/etc", "/var", "/root", "/home", "/opt", "/sys", "/proc", "/dev", "/boot",
			}
			for _, sp := range sensitivePrefixes {
				if strings.HasPrefix(path, sp) || strings.Contains(path, " "+sp) {
					return true
				}
			}
		}
	}

	return false
}
