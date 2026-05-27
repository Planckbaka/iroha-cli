package agent

import (
	"context"
	"fmt"
	"iter"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"iroha/pkg/llm"

	"github.com/firebase/genkit/go/core/api"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/anthropic"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// runnerHooks implements llm.AdapterHooks using the agent package's global managers.
type runnerHooks struct{}

func (runnerHooks) NagReminder() string {
	if GlobalTodoManager.RoundsSinceUpdate() >= 3 {
		return "📌 [System] To ensure continuity of subsequent code changes, please update your todo plan progress before executing the current step."
	}
	return ""
}

func (runnerHooks) NoteRound() {
	GlobalTodoManager.NoteRoundWithoutUpdate()
}

func buildSystemPrompt() string {
	builder := NewSystemPromptBuilder()
	return builder.Build()
}

// GlobalSessionService is the persistent session store wrapper singleton.
var GlobalSessionService *PersistentSessionService

// globalLLMModel is the current LLM model adapter for dynamic prompts/explanations.
var globalLLMModel model.LLM

// DynamicLLMDelegator is a thread-safe delegator that allows changing the active model at runtime.
type DynamicLLMDelegator struct {
	mu           sync.RWMutex
	currentModel model.LLM
}

func (d *DynamicLLMDelegator) Name() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentModel.Name()
}

func (d *DynamicLLMDelegator) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	d.mu.RLock()
	m := d.currentModel
	d.mu.RUnlock()
	return m.GenerateContent(ctx, req, stream)
}

func (d *DynamicLLMDelegator) SetModel(m model.LLM) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.currentModel = m
}

func (d *DynamicLLMDelegator) CumulativeTokens() int {
	d.mu.RLock()
	m := d.currentModel
	d.mu.RUnlock()
	if tt, ok := m.(llm.TokenTracker); ok {
		return tt.CumulativeTokens()
	}
	return 0
}

func (d *DynamicLLMDelegator) AddTokens(n int) {
	d.mu.Lock()
	m := d.currentModel
	d.mu.Unlock()
	if tt, ok := m.(llm.TokenTracker); ok {
		tt.AddTokens(n)
	}
}

// CustomRunner wraps ADK runner and manages background execution
type CustomRunner struct {
	mu              sync.RWMutex
	adkRunner       *runner.Runner
	llmModel        model.LLM
	delegator       *DynamicLLMDelegator
	Provider        llm.ProviderType
	ActiveModelName string
	APIKey          string
	BaseURL         string
	APIFormat       llm.APIFormat
	GenkitRegistry  *genkit.Genkit
}

// initGenkit creates a Genkit registry with the appropriate provider plugin.
// Returns nil for providers that use the direct adapter (e.g. OpenAI-compatible).
func initGenkit(provider llm.ProviderType, apiKey, baseURL string) *genkit.Genkit {
	switch provider {
	case llm.ProviderGemini, llm.ProviderClaude:
		ctx := context.Background()
		var plugins []api.Plugin
		switch provider {
		case llm.ProviderGemini:
			plugins = append(plugins, &googlegenai.GoogleAI{APIKey: apiKey})
		case llm.ProviderClaude:
			plugins = append(plugins, &anthropic.Anthropic{APIKey: apiKey, BaseURL: baseURL})
		}
		return genkit.Init(ctx, genkit.WithPlugins(plugins...))
	}
	return nil
}

func NewCustomRunner(provider llm.ProviderType, modelName string, apiKey string, baseURL string, apiFormat llm.APIFormat) (*CustomRunner, error) {
	// 1. Initialize Genkit registry (nil for OpenAI-compatible providers)
	g := initGenkit(provider, apiKey, baseURL)

	// 2. Create our abstract model adapter
	systemPrompt := buildSystemPrompt()
	modelAdapter, err := llm.NewAdapter(g, provider, modelName, apiKey, baseURL, systemPrompt, apiFormat, runnerHooks{})
	if err != nil {
		return nil, fmt.Errorf("failed to create model adapter: %w", err)
	}

	// 2. Load classic SWE tools
	tools, err := GetSWETools()
	if err != nil {
		return nil, fmt.Errorf("failed to load tool set: %w", err)
	}

	// 3. Setup tool with custom confirmation provider that blocks on the Bridge
	wrappedTools := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		// Wrap all tools to run through the permission checking pipeline
		wrappedTools = append(wrappedTools, &blockingConfirmationTool{
			Tool: t,
		})
	}

	// 4. Create llmagent — inject persistent memories into the system instruction
	baseInstruction := "" // now built dynamically by SystemPromptBuilder in prompt.go

	// s09: Append any durable memories that survived from previous sessions.
	// "Memory gives direction; current observation gives truth."
	instruction := baseInstruction
	if memSection := GlobalMemoryManager.BuildSystemPromptSection(); memSection != "" {
		instruction = baseInstruction + "\n\n" + memSection
	}

	delegator := &DynamicLLMDelegator{currentModel: modelAdapter}

	rootAgent, err := llmagent.New(llmagent.Config{
		Name:        "iroha-agent",
		Instruction: instruction,
		Model:       delegator,
		Tools:       wrappedTools,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	// 5. Create persistent session service
	inMem := session.InMemoryService()
	GlobalSessionService = NewPersistentSessionService(inMem, GetSessionsDir())

	// Pre-load all sessions from disk
	if err := GlobalSessionService.LoadSessions(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to restore historical sessions: %v\n", err)
	}

	// 6. Create ADK Runner
	adkRunner, err := runner.New(runner.Config{
		AppName:           "iroha",
		Agent:             rootAgent,
		SessionService:    GlobalSessionService,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	// 7. Fire SessionStart hooks — runs external scripts once at startup
	GlobalHookManager.RunHooks(HookSessionStart, HookContext{})

	// Initialize debug logging for LLM adapter
	llm.InitDebugLog()

	// 8. Configure auto-review with the model adapter
	SetAutoReviewConfig(modelAdapter)

	// Start background CronScheduler
	GlobalCronScheduler.Start()

	// Initialize GlobalAgentPool active parameters
	GlobalAgentPool.mu.Lock()
	GlobalAgentPool.Provider = provider
	GlobalAgentPool.ModelName = modelName
	GlobalAgentPool.APIKey = apiKey
	GlobalAgentPool.BaseURL = baseURL
	GlobalAgentPool.APIFormat = apiFormat
	GlobalAgentPool.GenkitRegistry = g
	GlobalAgentPool.mu.Unlock()

	// Override team ProcessMessage callback to use our agent pool
	GlobalTeamManager.ProcessMessage = func(teammate *Teammate, msg TeamMessage) (string, error) {
		return GlobalAgentPool.ExecuteMessage(teammate, msg)
	}

	globalLLMModel = modelAdapter

	// Trigger non-blocking automatic memory consolidation pass ("Dream Pass") in background
	if GlobalDreamConsolidator != nil {
		go func() {
			_, _ = GlobalDreamConsolidator.Consolidate(GlobalMemoryManager, false)
		}()
	}

	return &CustomRunner{
		adkRunner:       adkRunner,
		llmModel:        modelAdapter,
		delegator:       delegator,
		Provider:        provider,
		ActiveModelName: modelName,
		APIKey:          apiKey,
		BaseURL:         baseURL,
		APIFormat:       apiFormat,
		GenkitRegistry:  g,
	}, nil
}

func (cr *CustomRunner) SwitchModel(provider llm.ProviderType, modelName string, apiKey string, baseURL string, apiFormat llm.APIFormat) error {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	g := initGenkit(provider, apiKey, baseURL)

	systemPrompt := buildSystemPrompt()
	newAdapter, err := llm.NewAdapter(g, provider, modelName, apiKey, baseURL, systemPrompt, apiFormat, runnerHooks{})
	if err != nil {
		return fmt.Errorf("failed to create model adapter: %w", err)
	}

	cr.delegator.SetModel(newAdapter)
	cr.llmModel = newAdapter
	cr.Provider = provider
	cr.ActiveModelName = modelName
	cr.APIKey = apiKey
	cr.BaseURL = baseURL
	cr.APIFormat = apiFormat
	cr.GenkitRegistry = g

	GlobalAgentPool.mu.Lock()
	GlobalAgentPool.Provider = provider
	GlobalAgentPool.ModelName = modelName
	GlobalAgentPool.APIKey = apiKey
	GlobalAgentPool.BaseURL = baseURL
	GlobalAgentPool.APIFormat = apiFormat
	GlobalAgentPool.GenkitRegistry = g
	GlobalAgentPool.mu.Unlock()

	SetAutoReviewConfig(newAdapter)
	globalLLMModel = newAdapter

	return nil
}

func (cr *CustomRunner) ModelName() string {
	if cr.llmModel == nil {
		return "Unknown"
	}
	return cr.llmModel.Name()
}

func (cr *CustomRunner) GetTokenUsage() int {
	if cr.llmModel == nil {
		return 0
	}
	if adapter, ok := cr.llmModel.(llm.TokenTracker); ok {
		tokens := adapter.CumulativeTokens()
		if tokens > 0 {
			return tokens
		}
	}
	return 0
}

// Execute handles running a prompt asynchronously and piping events to a callback
func (cr *CustomRunner) Execute(ctx context.Context, userID, sessionID, prompt string, onEvent func(*session.Event), onError func(error), onDone func()) {
	GlobalToolCircuitBreaker.Reset()
	GlobalLogger.SetSessionID(sessionID)

	LogAudit(CatUserInput, "user_prompt", "User submitted a prompt to the agent", map[string]any{
		"user_id":    userID,
		"session_id": sessionID,
		"prompt":     prompt,
	})

	// Reset the cancel channel for this execution turn
	Bridge.Reset()
	go func() {
		<-ctx.Done()
		Bridge.Cancel()
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				rollbackPendingEdits()
				err := fmt.Errorf("panic in agent execution: %v\n%s", r, debug.Stack())
				LogError(CatSystem, "runner_panic", "Agent execution panicked", err, map[string]any{
					"session_id": sessionID,
				})
				onError(err)
				onDone()
			}
		}()

		// Drain background task notifications
		bgNotifs := GlobalBackgroundManager.DrainNotifications()
		// Drain cron scheduler notifications
		cronNotifs := GlobalCronScheduler.DrainNotifications()

		var sb strings.Builder
		if len(bgNotifs) > 0 {
			sb.WriteString("<background-results>\n")
			for _, n := range bgNotifs {
				sb.WriteString(fmt.Sprintf("  <task id=\"%s\" status=\"%s\" command=\"%s\">\n", n.TaskID, n.Status, n.Command))
				sb.WriteString(fmt.Sprintf("    <preview>%s</preview>\n", n.Preview))
				sb.WriteString(fmt.Sprintf("    <output_file>%s</output_file>\n", n.OutputFile))
				sb.WriteString("  </task>\n")
			}
			sb.WriteString("</background-results>\n\n")
		}

		if len(cronNotifs) > 0 {
			sb.WriteString("<scheduled-results>\n")
			for _, n := range cronNotifs {
				missedAttr := ""
				if n.MissedAt != "" {
					missedAttr = fmt.Sprintf(" missed_at=\"%s\"", n.MissedAt)
				}
				sb.WriteString(fmt.Sprintf("  <trigger id=\"%s\"%s>\n", n.ScheduleID, missedAttr))
				sb.WriteString(fmt.Sprintf("    <prompt>%s</prompt>\n", n.Prompt))
				sb.WriteString("  </trigger>\n")
			}
			sb.WriteString("</scheduled-results>\n\n")
		}

		if sb.Len() > 0 {
			prompt = sb.String() + prompt
		}

		// Fire HookUserPrompt before sending to LLM
		hookUserResult := GlobalHookManager.RunHooks(HookUserPrompt, HookContext{
			Prompt:    prompt,
			SessionID: sessionID,
		})
		if hookUserResult.Blocked {
			LogAudit(CatUserInput, "user_prompt_blocked", "User prompt blocked by hook", map[string]any{
				"session_id": sessionID,
				"reason":     hookUserResult.BlockReason,
			})
			onError(fmt.Errorf("prompt blocked by hook: %s", hookUserResult.BlockReason))
			onDone()
			return
		}
		// If hooks injected messages, prepend them to the prompt
		if len(hookUserResult.Messages) > 0 {
			prompt = strings.Join(hookUserResult.Messages, "\n") + "\n\n" + prompt
		}

		userMsg := &genai.Content{
			Role: "user",
			Parts: []*genai.Part{
				{Text: prompt},
			},
		}

		runConfig := runner.WithStateDelta(nil)
		events := cr.adkRunner.Run(ctx, userID, sessionID, userMsg, agent.RunConfig{
			StreamingMode: agent.StreamingModeSSE,
		}, runConfig)

		var responseTextLen int
		for ev, err := range events {
			if ctx.Err() != nil {
				rollbackPendingEdits()
				return
			}
			if err != nil {
				LogError(CatSystem, "runner_event_error", "Error received during agent run loop event streaming", err, map[string]any{
					"session_id": sessionID,
				})
				onError(err)
				return
			}
			if ev != nil {
				// Track response length for HookAgentResponse
				if ev.Content != nil {
					for _, p := range ev.Content.Parts {
						if p != nil && p.Text != "" {
							responseTextLen += len(p.Text)
						}
					}
				}
				onEvent(ev)
			}
		}

		// Fire HookAgentResponse after LLM response is fully received
		GlobalHookManager.RunHooks(HookAgentResponse, HookContext{
			ResponseLength: responseTextLen,
			SessionID:      sessionID,
		})

		commitPendingEdits()

		LogInfo(CatSystem, "runner_complete", "Agent execution completed successfully", map[string]any{
			"session_id": sessionID,
		})

		// Trigger Aider-style Git Auto-Commit if repository has staged/unstaged changes
		if hasChanges, err := GitHasChanges(); err == nil && hasChanges {
			if diffStr, err := GitGetStagedDiff(); err == nil && strings.TrimSpace(diffStr) != "" {
				if len(diffStr) > 8000 {
					diffStr = diffStr[:8000]
				}

				gitPrompt := fmt.Sprintf(`You are a professional Git commit assistant. Generate a concise, semantic Git Commit Message based on the following code changes (Git Diff).

		Requirements:
		1. Must use semantic commit conventions (e.g. feat: ..., fix: ..., chore: ..., refactor: ..., test: ...).
		2. Must be within 50 characters.
		3. Must return only the commit message itself, without any markdown markers, quotes, paragraphs, or explanatory text.

		[Code Changes (Git Diff)]:
		%s`, diffStr)

				req := &model.LLMRequest{
					Contents: []*genai.Content{
						{
							Role: "user",
							Parts: []*genai.Part{
								{Text: gitPrompt},
							},
						},
					},
				}

				var commitMsgBuilder strings.Builder
				events := cr.llmModel.GenerateContent(ctx, req, false)
				for resp, err := range events {
					if err == nil && resp != nil && resp.Content != nil && len(resp.Content.Parts) > 0 {
						commitMsgBuilder.WriteString(resp.Content.Parts[0].Text)
					}
				}

				commitMsg := strings.TrimSpace(commitMsgBuilder.String())
				commitMsg = strings.Trim(commitMsg, "\"`'")
				if commitMsg == "" {
					commitMsg = "chore: update files by iroha"
				}

				fullCommitMsg := fmt.Sprintf("[iroha] %s", commitMsg)
				if commitErr := GitCommit(fullCommitMsg); commitErr == nil {
					LogInfo(CatSystem, "git_auto_commit", fmt.Sprintf("Aider-style Git auto-commit completed: %s", fullCommitMsg), map[string]any{
						"session_id": sessionID,
						"msg":        fullCommitMsg,
					})
				} else {
					LogError(CatSystem, "git_auto_commit_failed", "Aider-style Git auto-commit failed", commitErr, map[string]any{
						"session_id": sessionID,
					})
				}
			}
		}

		// Fire HookSessionEnd before signaling completion
		GlobalHookManager.RunHooks(HookSessionEnd, HookContext{
			SessionID: sessionID,
		})

		onDone()
	}()
}
