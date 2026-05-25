package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iroha/pkg/llm"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// AutoReviewResult represents the LLM's safety judgment for a shell command
type AutoReviewResult struct {
	Safe   bool   // true = safe to auto-approve, false = needs human review
	Reason string // human-readable explanation
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

	// 5. Safe read-only commands
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
