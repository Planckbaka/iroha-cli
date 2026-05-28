package agent

import (
	"context"
	"fmt"
	"iter"
	"os"
	"sync"

	"iroha/pkg/llm"

	"github.com/firebase/genkit/go/core/api"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/anthropic"
	"github.com/firebase/genkit/go/plugins/googlegenai"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// runnerHooks implements llm.AdapterHooks using an injected TodoManager.
type runnerHooks struct {
	todo *TodoManager
}

func (h runnerHooks) NagReminder() string {
	if h.todo.RoundsSinceUpdate() >= 3 {
		return "📌 [System] To ensure continuity of subsequent code changes, please update your todo plan progress before executing the current step."
	}
	return ""
}

func (h runnerHooks) NoteRound() {
	h.todo.NoteRoundWithoutUpdate()
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

// RunnerDeps holds all manager dependencies injected into CustomRunner.
// Replaces direct global access within runner methods.
type RunnerDeps struct {
	TodoManager        *TodoManager
	MemoryManager      *MemoryManager
	SessionService     *PersistentSessionService
	BackgroundManager  *BackgroundManager
	CronScheduler      *CronScheduler
	HookManager        *HookManager
	Logger             *LoggerManager
	ToolCircuitBreaker *ToolCircuitBreaker
	AgentPool          *AgentPool
	TeamManager        *TeamManager
	DreamConsolidator  *DreamConsolidator
	AutoReviewConfig   *autoReviewConfig
	PermissionManager  *PermissionManager
	TaskManager        *TaskManager
	MCPRouter          *MCPToolRouter
	Bridge             *ConfirmationBridge
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
	deps            RunnerDeps
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
	modelAdapter, err := llm.NewAdapter(g, provider, modelName, apiKey, baseURL, systemPrompt, apiFormat, runnerHooks{todo: GlobalTodoManager})
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
		deps: RunnerDeps{
			TodoManager:        GlobalTodoManager,
			MemoryManager:      GlobalMemoryManager,
			SessionService:     GlobalSessionService,
			BackgroundManager:  GlobalBackgroundManager,
			CronScheduler:      GlobalCronScheduler,
			HookManager:        GlobalHookManager,
			Logger:             GlobalLogger,
			ToolCircuitBreaker: GlobalToolCircuitBreaker,
			AgentPool:          GlobalAgentPool,
			TeamManager:        GlobalTeamManager,
			DreamConsolidator:  GlobalDreamConsolidator,
			AutoReviewConfig:   GlobalAutoReviewConfig,
			PermissionManager:  GlobalPermissionManager,
			TaskManager:        GlobalTaskManager,
			MCPRouter:          GlobalMCPRouter,
				Bridge:             Bridge,
		},
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
	cr.deps.AgentPool = GlobalAgentPool

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

