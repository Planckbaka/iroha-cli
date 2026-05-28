package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"iroha/pkg/llm"

	"github.com/firebase/genkit/go/genkit"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// contextKey is a custom type for context values to avoid collisions
type contextKey string

const (
	WorkdirKey   contextKey = "workdir"
	AgentNameKey contextKey = "agent_name"
)

type AgentPool struct {
	mu             sync.RWMutex
	runners        map[string]*runner.Runner
	Provider       llm.ProviderType
	ModelName      string
	APIKey         string
	BaseURL        string
	APIFormat      llm.APIFormat
	GenkitRegistry *genkit.Genkit
}

var GlobalAgentPool = &AgentPool{
	runners: make(map[string]*runner.Runner),
}

// typePromptTemplates provides per-type system prompt prefixes for typed subagents.
var typePromptTemplates = map[string]string{
	"explore":    "You are an exploration agent. You can only read files and search. Report findings concisely.\n\n",
	"planner":    "You are a planning agent. You can only read files and search. Create detailed implementation plans.\n\n",
	"reviewer":   "You are a code review agent. You can only read files and search. Review code for bugs, style, and quality.\n\n",
	"executor":   "You are an execution agent with full capabilities. Implement changes according to instructions.\n\n",
	"researcher": "You are a research agent. You can read files and search. Gather and synthesize information.\n\n",
}

// allowedToolsByType defines which tool names each agent type can access.
var allowedToolsByType = map[string]map[string]bool{
	"explore":    {"file_read": true, "list_directory": true, "search_grep": true, "find_files": true},
	"planner":    {"file_read": true, "list_directory": true, "search_grep": true, "find_files": true},
	"reviewer":   {"file_read": true, "search_grep": true, "find_files": true},
	"researcher": {"file_read": true, "list_directory": true, "search_grep": true, "find_files": true},
}

// GetToolsForType returns a curated set of tools based on the agent type.
// Empty or unknown types return the full tool set for backward compatibility.
func GetToolsForType(typeName string) ([]tool.Tool, error) {
	allTools, err := GetSWETools()
	if err != nil {
		return nil, err
	}

	allowed, ok := allowedToolsByType[typeName]
	if !ok {
		// executor, empty, or unknown: return all tools
		return allTools, nil
	}

	filtered := make([]tool.Tool, 0, len(allowed))
	for _, t := range allTools {
		if allowed[t.Name()] {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// TypePromptPrefix returns the system prompt prefix for a given agent type.
// Returns empty string for unknown types.
func TypePromptPrefix(typeName string) string {
	if prefix, ok := typePromptTemplates[typeName]; ok {
		return prefix
	}
	return ""
}

func (ap *AgentPool) ExecuteMessage(teammate *Teammate, msg TeamMessage) (string, error) {
	ap.mu.Lock()
	subRunner, exists := ap.runners[teammate.Name]
	if exists {
		ap.mu.Unlock()
	} else {
		// Create runner while holding the lock to prevent duplicates.
		// Release lock only for heavy I/O work, then re-acquire to insert.

		// 1. Ensure worktree directory exists dynamically if it doesn't yet
		worktreePath := filepath.Join(GlobalWorktreeManager.worktreesDir, teammate.Name)
		ap.mu.Unlock()
		if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
			_, _ = GlobalWorktreeManager.Create(teammate.Name, "")
		}

		// 2. Setup subagent ADK Runner
		tools, err := GetToolsForType(teammate.Type)
		if err != nil {
			return "", err
		}

		wrappedTools := make([]tool.Tool, 0, len(tools))
		for _, t := range tools {
			wrappedTools = append(wrappedTools, &blockingConfirmationTool{Tool: t})
		}

		systemPrompt := TypePromptPrefix(teammate.Type) + teammate.SystemPrompt

		ap.mu.RLock()
		prov := ap.Provider
		mod := ap.ModelName
		key := ap.APIKey
		base := ap.BaseURL
		fmtFormat := ap.APIFormat
		genkitReg := ap.GenkitRegistry
		ap.mu.RUnlock()

		subAdapter, err := llm.NewAdapter(genkitReg, prov, mod, key, base, systemPrompt, fmtFormat, runnerHooks{})
		if err != nil {
			return "", err
		}

		subAgent, err := llmagent.New(llmagent.Config{
			Name:        teammate.Name,
			Instruction: systemPrompt,
			Model:       subAdapter,
			Tools:       wrappedTools,
		})
		if err != nil {
			return "", err
		}

		subSessionService := session.InMemoryService()
		subRunner, err = runner.New(runner.Config{
			AppName:           "iroha-subagent",
			Agent:             subAgent,
			SessionService:    subSessionService,
			AutoCreateSession: true,
		})
		if err != nil {
			return "", err
		}

		ap.mu.Lock()
		// Double-check: another goroutine may have created a runner for this teammate
		if existing, ok := ap.runners[teammate.Name]; ok {
			subRunner = existing
		} else {
			ap.runners[teammate.Name] = subRunner
		}
		ap.mu.Unlock()
	}

	// Determine worktree directory
	worktreePath := filepath.Join(GlobalWorktreeManager.worktreesDir, teammate.Name)
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		worktreePath, _ = os.Getwd()
	}

	// 3. Execute prompt on the subRunner
	// Setup context with subagent name and workdir path
	ctx := context.WithValue(context.Background(), WorkdirKey, worktreePath)
	ctx = context.WithValue(ctx, AgentNameKey, teammate.Name)

	userMsg := &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			{Text: msg.Content},
		},
	}

	runConfig := runner.WithStateDelta(nil)
	events := subRunner.Run(ctx, "subagent-user", teammate.Name+"-session", userMsg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}, runConfig)

	var responseBuilder strings.Builder
	for ev, err := range events {
		if err != nil {
			return "", err
		}
		if ev != nil && ev.Content != nil {
			for _, part := range ev.Content.Parts {
				if part.Text != "" {
					responseBuilder.WriteString(part.Text)
				}
			}
		}
	}

	return responseBuilder.String(), nil
}
