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

func (ap *AgentPool) ExecuteMessage(teammate *Teammate, msg TeamMessage) (string, error) {
	ap.mu.Lock()
	subRunner, exists := ap.runners[teammate.Name]
	ap.mu.Unlock()

	if !exists {
		// 1. Ensure worktree directory exists dynamically if it doesn't yet
		worktreePath := filepath.Join(GlobalWorktreeManager.worktreesDir, teammate.Name)
		if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
			_, _ = GlobalWorktreeManager.Create(teammate.Name, "")
		}

		// 2. Setup subagent ADK Runner
		// Retrieve SWE tools
		tools, err := GetSWETools()
		if err != nil {
			return "", err
		}

		wrappedTools := make([]tool.Tool, 0, len(tools))
		for _, t := range tools {
			wrappedTools = append(wrappedTools, &blockingConfirmationTool{Tool: t})
		}

		// Setup subagent model adapter with subagent system prompt
		ap.mu.RLock()
		prov := ap.Provider
		mod := ap.ModelName
		key := ap.APIKey
		base := ap.BaseURL
		fmtFormat := ap.APIFormat
		genkitReg := ap.GenkitRegistry
		ap.mu.RUnlock()

		// Retrieve or build subagent adapter
		subAdapter, err := llm.NewAdapter(genkitReg, prov, mod, key, base, teammate.SystemPrompt, fmtFormat, runnerHooks{})
		if err != nil {
			return "", err
		}

		subAgent, err := llmagent.New(llmagent.Config{
			Name:        teammate.Name,
			Instruction: teammate.SystemPrompt,
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
		ap.runners[teammate.Name] = subRunner
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
