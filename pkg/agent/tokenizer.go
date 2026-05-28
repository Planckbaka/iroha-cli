package agent

import (
	"fmt"
	"os"
	"strings"
)

// safePrefixes lists absolute path prefixes that are always allowed in shell commands.
// Configurable via the IROHA_SAFE_PREFIXES environment variable (comma-separated).
var safePrefixes []string

func init() {
	safePrefixes = []string{
		"/bin", "/usr", "/opt", "/tmp", "/dev",
		"/etc/resolv.conf", "/etc/hosts", "/etc/ssl",
		"/var/run", "/private/tmp",
	}
	if custom := os.Getenv("IROHA_SAFE_PREFIXES"); custom != "" {
		candidates := strings.Split(custom, ",")
		var valid []string
		for _, p := range candidates {
			p = strings.TrimSpace(p)
			if p == "" || p == "/" || len(p) < 3 {
				LogWarn(CatSecurity, "invalid_safe_prefix", "Rejecting invalid IROHA_SAFE_PREFIXES entry", map[string]any{"prefix": p})
				continue
			}
			valid = append(valid, p)
		}
		if len(valid) > 0 {
			safePrefixes = valid
		}
	}
}

// tokenizeCommand splits a shell command string into tokens, respecting quotes and
// backslash escapes. It also detects dangerous shell constructs (pipes, chaining,
// subshells, redirections) and returns an error for them.
func tokenizeCommand(command string) ([]string, error) {
	var tokens []string
	var buf strings.Builder
	i := 0
	n := len(command)

	for i < n {
		ch := command[i]

		switch {
		case ch == ' ' || ch == '\t':
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
			i++

		case ch == '"':
			i++
			for i < n {
				if command[i] == '\\' && i+1 < n {
					buf.WriteByte(command[i+1])
					i += 2
					continue
				}
				if command[i] == '"' {
					i++
					break
				}
				buf.WriteByte(command[i])
				i++
			}

		case ch == '\'':
			i++
			for i < n && command[i] != '\'' {
				buf.WriteByte(command[i])
				i++
			}
			if i < n {
				i++
			}

		case ch == '`':
			return nil, fmt.Errorf("security sandbox blocked: backtick subshell detected in command")

		case ch == '$' && i+1 < n && command[i+1] == '(':
			return nil, fmt.Errorf("security sandbox blocked: $() subshell detected in command")

		case ch == '|':
			return nil, fmt.Errorf("security sandbox blocked: pipe '|' detected in command")

		case ch == '&' && i+1 < n && command[i+1] == '&':
			return nil, fmt.Errorf("security sandbox blocked: command chaining '&&' detected in command")

		case ch == ';' :
			return nil, fmt.Errorf("security sandbox blocked: command separator ';' detected in command")

		case ch == '>':
			return nil, fmt.Errorf("security sandbox blocked: output redirection '>' detected in command")

		case ch == '<':
			return nil, fmt.Errorf("security sandbox blocked: input redirection '<' detected in command")

		default:
			if ch == '\\' && i+1 < n {
				buf.WriteByte(command[i+1])
				i += 2
			} else {
				buf.WriteByte(ch)
				i++
			}
		}
	}

	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}

	return tokens, nil
}
