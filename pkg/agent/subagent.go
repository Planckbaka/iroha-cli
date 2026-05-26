package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"iroha/pkg/llm"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// SubagentType defines the type of subagent
type SubagentType string

const (
	SubagentTypeExplore    SubagentType = "explore"    // Read-only filesystem exploration
	SubagentTypePlanner    SubagentType = "planner"    // Read-only planning/design
	SubagentTypeReviewer   SubagentType = "reviewer"   // Read-only code review
	SubagentTypeResearcher SubagentType = "researcher" // Read-only information gathering
	SubagentTypeExecutor   SubagentType = "executor"   // Modification-capable workspace executor
	SubagentTypeWork       SubagentType = "work"       // Alias for executor
)

// SubagentSpec holds the startup parameters for a subagent
type SubagentSpec struct {
	Name         string       `json:"name" description:"Unique subagent name (letters, numbers, underscores)"`
	Type         SubagentType `json:"type" description:"Subagent type: explore, planner, reviewer, researcher, executor"`
	Prompt       string       `json:"prompt" description:"Detailed instructions/tasks for the subagent"`
	ModelName    string       `json:"model_name,omitempty" description:"Optional cheaper/faster model override"`
}

// SubagentResult represents the structured completion result returned to the parent
type SubagentResult struct {
	Success      bool     `json:"success"`
	Summary      string   `json:"summary"`
	FilesCreated []string `json:"files_created,omitempty"`
	FilesEdited  []string `json:"files_edited,omitempty"`
	CommandsRun  []string `json:"commands_run,omitempty"`
	LogPath      string   `json:"log_path,omitempty"`
}

// SubagentManager manages synchronous subagent execution
type SubagentManager struct {
	mu sync.RWMutex
}

// GlobalSubagentManager is the singleton subagent manager
var GlobalSubagentManager = &SubagentManager{}

// ResolveSubagentLogsDir locates the directory for detailed subagent execution logs
func ResolveSubagentLogsDir() string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findProjectRoot(wd)
	logsDir := filepath.Join(root, ".iroha", "subagents", "logs")
	_ = os.MkdirAll(logsDir, 0755)
	return logsDir
}

// RunSubagent executes a subagent synchronously, managing its context isolation and worktree lifecycle
func (sm *SubagentManager) RunSubagent(ctx context.Context, spec SubagentSpec) (SubagentResult, error) {
	if spec.Name == "" {
		return SubagentResult{Success: false}, fmt.Errorf("subagent name is required")
	}
	if spec.Prompt == "" {
		return SubagentResult{Success: false}, fmt.Errorf("subagent prompt is required")
	}

	// 1. Determine workspace isolation (modifying types get a Git Worktree)
	useWorktree := false
	if spec.Type == SubagentTypeExecutor || spec.Type == SubagentTypeWork || spec.Type == "" {
		useWorktree = true
	}

	worktreePath := ""
	if useWorktree {
		wtName := fmt.Sprintf("sub-%s-%d", spec.Name, time.Now().UnixNano()/1e6)
		entry, err := GlobalWorktreeManager.Create(wtName, "")
		if err != nil {
			return SubagentResult{Success: false}, fmt.Errorf("failed to create worktree isolation for subagent: %w", err)
		}
		worktreePath = entry.Path

		// Cleanup worktree on completion
		defer func() {
			_ = GlobalWorktreeManager.Closeout(wtName, "remove", false)
		}()
	} else {
		// Read-only subagents run directly in parent's workspace CWD
		wd, err := os.Getwd()
		if err != nil {
			wd = "."
		}
		worktreePath = findProjectRoot(wd)
	}

	// 2. Setup curated toolset based on agent type
	tools, err := GetToolsForType(string(spec.Type))
	if err != nil {
		return SubagentResult{Success: false}, fmt.Errorf("failed to retrieve tools for subagent type: %w", err)
	}

	wrappedTools := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		wrappedTools = append(wrappedTools, &blockingConfirmationTool{Tool: t})
	}

	// 3. Build subagent model adapter with customized routing (e.g. cheaper model)
	GlobalAgentPool.mu.RLock()
	provider := GlobalAgentPool.Provider
	modelName := GlobalAgentPool.ModelName
	apiKey := GlobalAgentPool.APIKey
	baseURL := GlobalAgentPool.BaseURL
	apiFormat := GlobalAgentPool.APIFormat
	genkitReg := GlobalAgentPool.GenkitRegistry
	GlobalAgentPool.mu.RUnlock()

	// If specified custom cheaper model, override the active modelName
	if spec.ModelName != "" {
		modelName = spec.ModelName
	} else {
		// Default to a fast/cheap fallback depending on provider
		switch provider {
		case llm.ProviderClaude:
			modelName = "claude-3-5-haiku"
		case llm.ProviderGemini:
			modelName = "gemini-2.5-flash"
		case llm.ProviderOpenAI:
			modelName = "gpt-4o-mini"
		case llm.ProviderDeepSeek:
			modelName = "deepseek-chat"
		}
	}

	systemPrompt := TypePromptPrefix(string(spec.Type)) + buildSystemPrompt()
	subHooks := runnerHooks{} // reuse main hooks

	subAdapter, err := llm.NewAdapter(genkitReg, provider, modelName, apiKey, baseURL, systemPrompt, apiFormat, subHooks)
	if err != nil {
		return SubagentResult{Success: false}, fmt.Errorf("failed to initialize subagent model adapter: %w", err)
	}

	// 4. Create ADK Agent & Runner for subagent
	subAgent, err := llmagent.New(llmagent.Config{
		Name:        spec.Name,
		Instruction: systemPrompt,
		Model:       subAdapter,
		Tools:       wrappedTools,
	})
	if err != nil {
		return SubagentResult{Success: false}, fmt.Errorf("failed to create subagent: %w", err)
	}

	subSessionService := session.InMemoryService()
	subRunner, err := runner.New(runner.Config{
		AppName:           "iroha-subagent",
		Agent:             subAgent,
		SessionService:    subSessionService,
		AutoCreateSession: true,
	})
	if err != nil {
		return SubagentResult{Success: false}, fmt.Errorf("failed to create subagent runner: %w", err)
	}

	// 5. Setup executing context pointing to isolated workdir CWD
	subCtx := context.WithValue(ctx, WorkdirKey, worktreePath)
	subCtx = context.WithValue(subCtx, AgentNameKey, spec.Name)

	userMsg := &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			{Text: spec.Prompt},
		},
	}

	// 6. Run the subagent execution loop synchronously, listening to events and logging
	runConfig := runner.WithStateDelta(nil)
	events := subRunner.Run(subCtx, "subagent-user", spec.Name+"-sync-session", userMsg, agent.RunConfig{
		StreamingMode: agent.StreamingModeSSE,
	}, runConfig)

	// Open detailed session log file
	logsDir := ResolveSubagentLogsDir()
	logFileName := fmt.Sprintf("%s_%s_%d.jsonl", spec.Name, spec.Type, time.Now().UnixNano()/1e6)
	logPath := filepath.Join(logsDir, logFileName)
	logFile, logErr := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	var responseBuilder strings.Builder
	var filesCreated []string
	var filesEdited []string
	var commandsRun []string

	for ev, err := range events {
		if err != nil {
			if logErr == nil {
				_, _ = logFile.WriteString(fmt.Sprintf(`{"error": %q, "timestamp": %q}`+"\n", err.Error(), time.Now().Format(time.RFC3339)))
				_ = logFile.Close()
			}
			return SubagentResult{Success: false, LogPath: logPath}, err
		}

		if ev != nil {
			// Write event detail to log
			if logErr == nil {
				if eventData, err := json.Marshal(ev); err == nil {
					_, _ = logFile.Write(append(eventData, '\n'))
				}
			}

			// Accumulate response text
			if ev.Content != nil {
				for _, part := range ev.Content.Parts {
					if part.Text != "" {
						responseBuilder.WriteString(part.Text)
					}
				}
			}
		}
	}

	if logErr == nil {
		_ = logFile.Close()
	}

	// 7. Dynamic analysis of files in isolated worktree to find modifications
	if useWorktree {
		// Run git diff/status in worktree directory to accurately find modifications
		cmdStatus := genaiCommandInDir(worktreePath, "git", "status", "--porcelain")
		if output, err := cmdStatus.Output(); err == nil {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) < 2 {
					continue
				}
				status := parts[0]
				file := parts[1]
				if strings.Contains(status, "??") || strings.Contains(status, "A") {
					filesCreated = append(filesCreated, file)
				} else if strings.Contains(status, "M") {
					filesEdited = append(filesEdited, file)
				}
			}
		}
	}

	return SubagentResult{
		Success:      true,
		Summary:      responseBuilder.String(),
		FilesCreated: filesCreated,
		FilesEdited:  filesEdited,
		CommandsRun:  commandsRun,
		LogPath:      logPath,
	}, nil
}

// helper command runner
func genaiCommandInDir(dir, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd
}
