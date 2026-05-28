package agent

import (
	"regexp"
	"strings"
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
