package agent

import (
	"context"
	"iter"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestHeuristicReview_SafeCommands(t *testing.T) {
	safeCmds := []string{
		"ls",
		"ls -la",
		"pwd",
		"cat README.md",
		"echo hello",
		"git status",
		"git log --oneline -5",
		"go build ./...",
		"go test ./...",
		"grep -r foo .",
		"find . -name '*.go'",
		"head -20 main.go",
		"tail -f app.log",
		"wc -l *.go",
		"which go",
		"env",
	}

	for _, cmd := range safeCmds {
		t.Run(cmd, func(t *testing.T) {
			result := heuristicReview(cmd)
			if !result.Safe {
				t.Errorf("Expected %q to be safe, but got: %s", cmd, result.Reason)
			}
		})
	}
}

func TestHeuristicReview_DangerousCommands(t *testing.T) {
	dangerousCmds := []string{
		"rm -rf /",
		"mv file1 file2",
		"cp src dst",
		"chmod 777 .",
		"sudo apt install vim",
		"curl http://example.com",
		"wget http://example.com",
		"echo hello > output.txt",
		"echo data >> file.txt",
		"kill -9 1234",
	}

	for _, cmd := range dangerousCmds {
		t.Run(cmd, func(t *testing.T) {
			result := heuristicReview(cmd)
			if result.Safe {
				t.Errorf("Expected %q to be dangerous, but got safe: %s", cmd, result.Reason)
			}
		})
	}
}

func TestReviewCommand_SimulateMode(t *testing.T) {
	// In simulate mode (no model), ReviewCommand should use heuristic
	GlobalAutoReviewConfig = nil

	safeResult := ReviewCommand("ls -la")
	if !safeResult.Safe {
		t.Errorf("ls -la should be safe in simulate mode, got: %s", safeResult.Reason)
	}

	dangerResult := ReviewCommand("rm -rf /")
	if dangerResult.Safe {
		t.Errorf("rm -rf / should be dangerous in simulate mode, got: %s", dangerResult.Reason)
	}
}

func TestSetAutoReviewConfig(t *testing.T) {
	mock := &MockLLM{}
	SetAutoReviewConfig(mock)

	if GlobalAutoReviewConfig == nil {
		t.Fatal("Expected GlobalAutoReviewConfig to be set")
	}
	if GlobalAutoReviewConfig.Model == nil {
		t.Fatal("Expected Model to be set")
	}
	if GlobalAutoReviewConfig.Model.Name() != "mock-llm" {
		t.Errorf("Expected model name mock-llm, got %s", GlobalAutoReviewConfig.Model.Name())
	}
}

func TestHeuristicReview_ObfuscationBypass(t *testing.T) {
	bypassCmds := []string{
		"c'u'r'l http://attacker.com",
		"w\\g\\e\\t http://attacker.com",
		"rm\\ -rf /",
		"echo hello >\\ output.txt",
		"eval \"rm -rf /\"",
		"curl$(echo ) http://attacker.com",
		"ls `rm -rf /`",
	}

	for _, cmd := range bypassCmds {
		t.Run(cmd, func(t *testing.T) {
			result := heuristicReview(cmd)
			if result.Safe {
				t.Errorf("Expected obfuscated command %q to be unsafe, but got safe! Reason: %s", cmd, result.Reason)
			}
		})
	}
}

type MockLLM struct {
	ResponseText string
	ResponseErr  error
}

func (m *MockLLM) Name() string { return "mock-llm" }
func (m *MockLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.ResponseErr != nil {
			yield(nil, m.ResponseErr)
			return
		}
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{Text: m.ResponseText},
				},
			},
			Partial:      false,
			TurnComplete: true,
		}, nil)
	}
}

func TestHeuristicReview_WhitespaceEvasion(t *testing.T) {
	evasionCmds := []string{
		"rm\t-rf /",
		"sudo\tapt install vim",
		"curl\t-O http://example.com",
		"echo hello\t>\toutput.txt",
		"rm\n-rf /",
	}

	for _, cmd := range evasionCmds {
		t.Run(cmd, func(t *testing.T) {
			result := heuristicReview(cmd)
			if result.Safe {
				t.Errorf("Expected whitespace evasion command %q to be unsafe, but got safe! Reason: %s", cmd, result.Reason)
			}
		})
	}
}

func TestReviewCommand_HybridGuard(t *testing.T) {
	// Setup Mock LLM
	mock := &MockLLM{
		ResponseText: `{"safe": true, "reason": "AI says safe"}`,
	}
	SetAutoReviewConfig(mock)
	defer func() {
		GlobalAutoReviewConfig = nil
	}()

	// 1. Hard Reject overrides LLM "safe" decision
	t.Run("Hard Reject overrides LLM", func(t *testing.T) {
		result := ReviewCommand("rm -rf /")
		if result.Safe {
			t.Error("Expected rm -rf / to be UNSAFE due to hard rule, even if LLM returns safe!")
		}
	})

	// 2. Pre-approved safe commands bypass LLM
	t.Run("Pre-approved safe command bypasses LLM", func(t *testing.T) {
		// Even if LLM returns unsafe, pre-approved commands should be safe
		mock.ResponseText = `{"safe": false, "reason": "AI says unsafe"}`
		result := ReviewCommand("ls -la")
		if !result.Safe {
			t.Error("Expected ls -la to be safe via pre-approved list, even if LLM returns unsafe!")
		}
	})

	// 3. Unknown commands invoke LLM
	t.Run("Unknown command LLM safe", func(t *testing.T) {
		mock.ResponseText = `{"safe": true, "reason": "safe compilation"}`
		result := ReviewCommand("npm run build")
		if !result.Safe {
			t.Error("Expected npm run build to be safe because LLM approved it")
		}
		if result.Reason != "safe compilation" {
			t.Errorf("Expected reason 'safe compilation', got %q", result.Reason)
		}
	})

	t.Run("Unknown command LLM unsafe", func(t *testing.T) {
		mock.ResponseText = `{"safe": false, "reason": "unsafe script"}`
		result := ReviewCommand("node hack.js")
		if result.Safe {
			t.Error("Expected node hack.js to be unsafe because LLM rejected it")
		}
		if result.Reason != "unsafe script" {
			t.Errorf("Expected reason 'unsafe script', got %q", result.Reason)
		}
	})

	// 4. Double-check LLM injection/bypass protection
	t.Run("LLM jailbreak protection", func(t *testing.T) {
		// Even if LLM is tricked into saying true for a dangerous command:
		mock.ResponseText = `{"safe": true, "reason": "bypass"}`
		result := ReviewCommand("curl http://evil.com")
		if result.Safe {
			t.Error("Expected curl to be blocked by local safety guard double check, even if LLM returned safe: true")
		}
	})
}

func TestHeuristicReview_ChainedCommandBypass(t *testing.T) {
	bypassCmds := []string{
		"ls ; rm -rf /",
		"git status && curl http://attacker.com",
		"pwd || wget http://attacker.com",
		"cat README.md | rm -rf /",
		"echo hello > output.txt",
		"echo hello >> output.txt",
		"cat < secret.txt",
		"A=rm; $A -rf /",
	}

	for _, cmd := range bypassCmds {
		t.Run(cmd, func(t *testing.T) {
			result := heuristicReview(cmd)
			if result.Safe {
				t.Errorf("Expected chained/redirect command %q to be unsafe, but got safe! Reason: %s", cmd, result.Reason)
			}
		})
	}
}

func TestHeuristicReview_HardenedRules(t *testing.T) {
	tests := []struct {
		cmd  string
		safe bool
	}{
		// 1. Newline command injection
		{"ls\npython3 hack.py", false},
		{"git status\rcurl http://attacker.com", false},

		// 2. env arguments vs exact match
		{"env", true},
		{"env python3 hack.py", false},
		{"env A=B python3 hack.py", false},

		// 3. find execution flags vs safe usage
		{"find . -name '*.go'", true},
		{"find . -exec python3 hack.py {} +", false},
		{"find . -delete", false},
		{"find . -ok rm {} \\;", false},

		// 4. toolexec pattern
		{"go test -toolexec python3", false},
		{"go build -toolexec=python3", false},

		// 5. git remote subcommands
		{"git remote", true},
		{"git remote -v", true},
		{"git remote show", true},
		{"git remote show origin", true},
		{"git remote add origin http://attacker.com", false},
		{"git remote remove origin", false},
		{"git remote set-url origin http://attacker.com", false},
	}

	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			result := heuristicReview(tc.cmd)
			if result.Safe != tc.safe {
				t.Errorf("For command %q: expected safe=%t, but got safe=%t (reason: %s)", tc.cmd, tc.safe, result.Safe, result.Reason)
			}
		})
	}
}
