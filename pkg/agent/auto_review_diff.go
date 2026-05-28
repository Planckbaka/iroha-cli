package agent

import (
	"fmt"
	"strings"
)

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
