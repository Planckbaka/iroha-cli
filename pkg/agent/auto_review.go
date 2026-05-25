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
const autoReviewSystemPrompt = `你是一个严格的安全审查员，负责评估 Shell 命令是否安全。
你的任务是判断一条 Shell 命令是否可以在用户的工作区安全执行，无需用户手动审批。

判断标准：
- SAFE（安全，可自动放行）：只读操作，例如 ls, pwd, cat, echo, git status, git log, go build, go test, find, grep, head, tail, wc, which, env
- UNSAFE（危险，需要人工审核）：任何写入、删除、网络请求、系统修改、权限变更等操作

回复格式必须严格为 JSON：
{"safe": true, "reason": "只读查看目录，无风险"}
或
{"safe": false, "reason": "删除操作，可能造成数据丢失"}

只返回 JSON，不要任何额外文字。`

// ReviewCommand asks the configured LLM whether a shell command is safe,
// but enforces a Hybrid Safety Guard: local heuristic safety rules act as an absolute
// hard filter and known safe check, while the LLM is only called for unknown custom commands.
func ReviewCommand(cmd string) AutoReviewResult {
	// 1. Run local heuristic review first
	heuristicResult := heuristicReview(cmd)

	// A. If the heuristic review detects bypass tricks or dangerous patterns, we HARD REJECT immediately.
	// We check this by seeing if heuristicReview returned Safe: false AND the reason indicates a hard rule block.
	// Specifically, if heuristicResult is UNSAFE, and it's NOT because of "未知或自定义命令", then it's a hard block!
	if !heuristicResult.Safe && !strings.Contains(heuristicResult.Reason, "未知或自定义命令") {
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
				Reason: "安全熔断：LLM 判定命令安全，但本地校验检测到换行符或多行指令风险",
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
				Reason: "安全熔断：LLM 判定命令安全，但本地校验检测到隐藏的危险模式或绕过风险",
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
					{Text: fmt.Sprintf("请审查以下 Shell 命令：\n```\n%s\n```", cmd)},
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
		return AutoReviewResult{Safe: false, Reason: "AI 审查响应格式错误，交由人工确认"}, nil
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
			Reason: "规则审查：检测到换行符或多行指令，存在级联执行风险，禁止自动运行",
		}
	}

	// 2. State-machine Tokenizer Command Chain Decoupling
	subcmds := tokenizeShellCommand(cmd)
	for _, sub := range subcmds {
		subNorm := normalizeCommand(sub)

		if strings.Contains(subNorm, "$(") || strings.Contains(subNorm, "`") {
			return AutoReviewResult{
				Safe:   false,
				Reason: "规则审查：检测到命令替换或子 Shell 嵌套，存在越权风险",
			}
		}
		if strings.Contains(subNorm, "eval ") || strings.Contains(subNorm, "exec ") {
			return AutoReviewResult{
				Safe:   false,
				Reason: "规则审查：检测到动态执行（eval/exec），禁止自动运行",
			}
		}

		if isPathDangerous(sub) {
			return AutoReviewResult{
				Safe:   false,
				Reason: "安全沙箱阻断：检测到命令行参数中含有目录逃逸或越界访问系统敏感路径的操作",
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
					Reason: fmt.Sprintf("规则审查：检测到危险系统命令 `%s`，禁止自动放行", cmdName),
				}
			}
		}
	}

	normalized := normalizeCommand(cmd)

	// 3. Shell metacharacter and redirection detection (chained commands, pipes, variables)
	if strings.ContainsAny(normalized, ";|&$<>`") {
		return AutoReviewResult{
			Safe:   false,
			Reason: "规则审查：检测到 Shell 元字符或重定向操作（;|&$<>`），禁止自动运行",
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
				Reason: fmt.Sprintf("规则审查：命令含有危险模式 `%s`，需要人工确认", strings.TrimSpace(pattern)),
			}
		}
	}

	// Find command execution flags check
	if strings.HasPrefix(normalized, "find ") || normalized == "find" {
		if strings.Contains(normalized, "-exec") || strings.Contains(normalized, "-ok") || strings.Contains(normalized, "-delete") {
			return AutoReviewResult{
				Safe:   false,
				Reason: "规则审查：检测到 `find` 命令含有危险执行或删除参数（-exec/-ok/-delete），禁止自动运行",
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
				Reason: fmt.Sprintf("规则审查：`%s` 是安全的只读命令", strings.Fields(normalized)[0]),
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
				Reason: fmt.Sprintf("规则审查：`%s` 是安全的只读命令", strings.Fields(normalized)[0]),
			}
		}
	}

	// Unknown — ask human
	return AutoReviewResult{
		Safe:   false,
		Reason: "规则审查：未知或自定义命令，交由人工确认",
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
