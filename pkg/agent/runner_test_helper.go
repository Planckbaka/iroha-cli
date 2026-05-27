package agent

import (
	"context"
	"iter"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
)

// testLLMModel is a no-op model.LLM implementation for tests.
type testLLMModel struct{}

func (testLLMModel) Name() string { return "test-model" }
func (testLLMModel) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {}
}

// NewTestRunner creates a minimal CustomRunner for testing without network calls.
// It uses a no-op LLM model, no Genkit, and no cron scheduler.
func NewTestRunner() (*CustomRunner, error) {
	modelAdapter := testLLMModel{}
	delegator := &DynamicLLMDelegator{currentModel: modelAdapter}

	// Create a minimal root agent with no tools
	rootAgent, err := llmagent.New(llmagent.Config{
		Name:        "iroha-test-agent",
		Instruction: "You are a test agent.",
		Model:       delegator,
	})
	if err != nil {
		return nil, err
	}

	// Create an in-memory session service (no disk persistence)
	inMem := session.InMemoryService()
	GlobalSessionService = NewPersistentSessionService(inMem, GetSessionsDir())

	adkRunner, err := runner.New(runner.Config{
		AppName:           "iroha-test",
		Agent:             rootAgent,
		SessionService:    GlobalSessionService,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, err
	}

	globalLLMModel = modelAdapter

	return &CustomRunner{
		adkRunner:       adkRunner,
		llmModel:        modelAdapter,
		delegator:       delegator,
		ActiveModelName: "test-model",
	}, nil
}
