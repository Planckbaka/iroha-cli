package agent

import (
	"testing"
)

func TestBashSecurityValidator(t *testing.T) {
	v := NewBashSecurityValidator()

	tests := []struct {
		name     string
		command  string
		expected bool // true if it should fail validation
	}{
		{"Safe Echo", "echo 'hello world'", false},
		{"Safe Cat", "cat pkg/agent/permission.go", false},
		{"Sudo Command", "sudo apt-get install git", true},
		{"Rm Rf Direct", "rm -rf /", true},
		{"Rm R Direct", "rm -r pkg", true},
		{"Shell Metacharacter Semicolon", "echo 'a'; rm -rf /", true},
		{"Shell Metacharacter Pipe", "cat file | grep password", true},
		{"Command Substitution", "echo $(whoami)", true},
		{"IFS Injection", "IFS=;echo", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := v.Validate(tt.command)
			hasFailures := len(failures) > 0
			if hasFailures != tt.expected {
				t.Errorf("Validate(%q) got failures = %v (%d failures), expected failure: %t", tt.command, failures, len(failures), tt.expected)
			}
		})
	}
}

func TestPermissionManagerModes(t *testing.T) {
	// 1. Default Mode Tests
	t.Run("Default Mode Pipeline", func(t *testing.T) {
		pm := NewPermissionManager(ModeDefault)

		// File read is allowed by default rule: {Tool: "file_read", Path: "*", Behavior: "allow"}
		decision, reason := pm.Check("file_read", FileReadArgs{Path: "main.go"})
		if decision != "allow" {
			t.Errorf("Expected 'allow' for file_read under default mode, got %q (reason: %q)", decision, reason)
		}

		// Shell run with safe command has no default allow/deny rule -> should ask
		decision, reason = pm.Check("shell_run", ShellRunArgs{Command: "go test ./..."})
		if decision != "ask" {
			t.Errorf("Expected 'ask' for safe shell_run, got %q (reason: %q)", decision, reason)
		}

		// Shell run with 'rm -rf /' matches default deny rule
		decision, reason = pm.Check("shell_run", ShellRunArgs{Command: "rm -rf /"})
		if decision != "deny" {
			t.Errorf("Expected 'deny' for rm -rf /, got %q (reason: %q)", decision, reason)
		}

		// Shell run with 'sudo' flags validator severe pattern -> deny
		decision, reason = pm.Check("shell_run", ShellRunArgs{Command: "sudo rm -rf"})
		if decision != "deny" {
			t.Errorf("Expected 'deny' for sudo, got %q (reason: %q)", decision, reason)
		}
	})

	// 2. Plan Mode Tests
	t.Run("Plan Mode Restrictions", func(t *testing.T) {
		pm := NewPermissionManager(ModePlan)

		// File read should follow normal allow rule -> allow
		decision, reason := pm.Check("file_read", FileReadArgs{Path: "main.go"})
		if decision != "allow" {
			t.Errorf("Expected 'allow' for file_read in Plan mode, got %q (reason: %q)", decision, reason)
		}

		// File write is a write tool -> blocked immediately in Plan mode
		decision, reason = pm.Check("file_write", FileWriteArgs{Path: "out.go", Content: "test"})
		if decision != "deny" {
			t.Errorf("Expected 'deny' for file_write in Plan mode, got %q (reason: %q)", decision, reason)
		}

		// Shell run is a write tool -> blocked immediately in Plan mode
		decision, reason = pm.Check("shell_run", ShellRunArgs{Command: "go test ./..."})
		if decision != "deny" {
			t.Errorf("Expected 'deny' for shell_run in Plan mode, got %q (reason: %q)", decision, reason)
		}
	})

	// 3. Auto Mode Tests (Phase 2: uses 4-tier risk classifier)
	t.Run("Auto Mode Permissions", func(t *testing.T) {
		pm := NewPermissionManager(ModeAuto)

		// TierTrusted: File read is a read-only tool -> auto-approved
		decision, reason := pm.Check("file_read", FileReadArgs{Path: "main.go"})
		if decision != "allow" {
			t.Errorf("Expected 'allow' for file_read in Auto mode, got %q (reason: %q)", decision, reason)
		}

		// TierTrusted: Todo is a known safe tool -> auto-approved
		decision, reason = pm.Check("todo", nil)
		if decision != "allow" {
			t.Errorf("Expected 'allow' for todo in Auto mode, got %q (reason: %q)", decision, reason)
		}

		// TierLowRisk: File write is low-risk -> auto-approved with logging
		decision, reason = pm.Check("file_write", FileWriteArgs{Path: "out.go", Content: "test"})
		if decision != "allow" {
			t.Errorf("Expected 'allow' for file_write in Auto mode (low_risk tier), got %q (reason: %q)", decision, reason)
		}

		// TierTrusted: shell_run with trusted command (ls) -> auto-approved
		decision, reason = pm.Check("shell_run", ShellRunArgs{Command: "ls -la"})
		if decision != "allow" {
			t.Errorf("Expected 'allow' for ls in Auto mode, got %q (reason: %q)", decision, reason)
		}

		// TierHighRisk: shell_run with dangerous command (rm) -> deny (caught by security validator)
		decision, reason = pm.Check("shell_run", ShellRunArgs{Command: "rm -rf /"})
		if decision != "deny" {
			t.Errorf("Expected 'deny' for rm -rf / in Auto mode, got %q (reason: %q)", decision, reason)
		}

		// TierHighRisk: unknown tool -> ask human
		decision, reason = pm.Check("unknown_tool", nil)
		if decision != "ask" {
			t.Errorf("Expected 'ask' for unknown tool in Auto mode, got %q (reason: %q)", decision, reason)
		}
	})
}

func TestDynamicRulesAndAlwaysAllow(t *testing.T) {
	pm := NewPermissionManager(ModeDefault)

	// A file_write normally asks because there is no matching rule in the default list
	decision, reason := pm.Check("file_write", FileWriteArgs{Path: "main.go", Content: "pkg"})
	if decision != "ask" {
		t.Errorf("Expected initial check for file_write to ask, got %q (reason: %q)", decision, reason)
	}

	// Dynamically add a temporary allow rule (simulating "always" option)
	pm.AddRule(PermissionRule{
		Tool:     "file_write",
		Behavior: "allow",
		Path:     "*",
	})

	// Now check again, should be approved dynamically
	decision, reason = pm.Check("file_write", FileWriteArgs{Path: "main.go", Content: "pkg"})
	if decision != "allow" {
		t.Errorf("Expected subsequent check for file_write to be allowed, got %q (reason: %q)", decision, reason)
	}
}

func TestDenialCountersAndCircuitBreaker(t *testing.T) {
	pm := NewPermissionManager(ModeDefault)

	if pm.ConsecutiveDenials() != 0 {
		t.Errorf("Expected initial denials to be 0, got %d", pm.ConsecutiveDenials())
	}

	// Note a denial
	d1 := pm.NoteDenial()
	if d1 != 1 || pm.ConsecutiveDenials() != 1 {
		t.Errorf("Expected denials to be 1, got d1=%d consecutive=%d", d1, pm.ConsecutiveDenials())
	}

	// Note another denial
	d2 := pm.NoteDenial()
	if d2 != 2 || pm.ConsecutiveDenials() != 2 {
		t.Errorf("Expected denials to be 2, got d2=%d consecutive=%d", d2, pm.ConsecutiveDenials())
	}

	// An approval should reset it
	pm.NoteApproval()
	if pm.ConsecutiveDenials() != 0 {
		t.Errorf("Expected approval to reset denials, got %d", pm.ConsecutiveDenials())
	}
}

func TestEnhancedBashSecurityValidator(t *testing.T) {
	v := NewBashSecurityValidator()

	tests := []struct {
		name     string
		command  string
		expected bool // true if it should fail validation
	}{
		{"Heredoc Pattern", "cat <<EOF\nhello\nEOF", true},
		{"Named Pipe", "mkfifo /tmp/pipe", true},
		{"Proxy Injection", "git -c core.sshCommand=evilCommand fetch", true},
		{"Unsafe Find Pipe", "find . -name '*.log' | while read file; do rm $file; done", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failures := v.Validate(tt.command)
			hasFailures := len(failures) > 0
			if hasFailures != tt.expected {
				t.Errorf("Validate(%q) got failures = %v (%d failures), expected failure: %t", tt.command, failures, len(failures), tt.expected)
			}
		})
	}
}

func TestMatchesPatternWildcardGlob(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		value    string
		expected bool
	}{
		{"Exact match", "pkg/agent", "pkg/agent", true},
		{"Exact match case insensitive", "pkg/agent", "PKG/AGENT", true},
		{"Wildcard end", "pkg/*", "pkg/agent/permission.go", true},
		{"Wildcard end mismatch", "pkg/*", "other/agent/permission.go", false},
		{"Wildcard start", "*.go", "main.go", true},
		{"Wildcard start mismatch", "*.go", "main.py", false},
		{"Wildcard middle", "pkg/*/agent", "pkg/test/agent", true},
		{"Wildcard middle mismatch", "pkg/*/agent", "pkg/test/other", false},
		{"Multiple wildcards", "pkg/*/agent/*.go", "pkg/test/agent/permission.go", true},
		{"Substring fallback", "agent", "pkg/agent/permission.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesPattern(tt.pattern, tt.value)
			if result != tt.expected {
				t.Errorf("matchesPattern(%q, %q) got %t, expected %t", tt.pattern, tt.value, result, tt.expected)
			}
		})
	}
}

