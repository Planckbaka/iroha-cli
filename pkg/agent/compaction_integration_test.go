package agent

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

type mockLLM struct {
	generateFunc func(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error]
}

func (m *mockLLM) Name() string {
	return "mock-llm"
}

func (m *mockLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if m.generateFunc != nil {
		return m.generateFunc(ctx, req, stream)
	}
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{Text: "LLM-generated summary"},
				},
			},
			TurnComplete: true,
		}, nil)
	}
}

func TestCompaction_LLMAndCircuitBreaker(t *testing.T) {
	// Reset circuit breaker state before testing
	compactionCircuitBreaker.mu.Lock()
	compactionCircuitBreaker.failures = 0
	compactionCircuitBreaker.open = false
	compactionCircuitBreaker.mu.Unlock()

	// 1. Create a history of 14 rounds (>12)
	contents := make([]*genai.Content, 14)
	for i := 0; i < 14; i++ {
		role := "user"
		if i%2 == 1 {
			role = "model"
		}
		contents[i] = &genai.Content{
			Role: role,
			Parts: []*genai.Part{
				{Text: fmt.Sprintf("Round message %d", i)},
			},
		}
	}

	// 2. Test successful LLM summarization
	mock := &mockLLM{}
	compacted := CompactContents(contents, "session-test-llm", mock)
	if len(compacted) != 6 {
		t.Fatalf("Expected compacted length 6, got %d", len(compacted))
	}
	// Verify that the second item is the system prompt with LLM summary
	sysText := compacted[1].Parts[0].Text
	if !strings.Contains(sysText, "LLM-generated summary") {
		t.Errorf("Expected summary to contain LLM output, got: %q", sysText)
	}

	// Verify circuit breaker did not trip
	compactionCircuitBreaker.mu.Lock()
	failures := compactionCircuitBreaker.failures
	isOpen := compactionCircuitBreaker.open
	compactionCircuitBreaker.mu.Unlock()
	if failures != 0 || isOpen {
		t.Errorf("Expected 0 failures and closed circuit breaker, got failures=%d, open=%v", failures, isOpen)
	}

	// 3. Test failing LLM calls to trip circuit breaker
	failMock := &mockLLM{
		generateFunc: func(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
			panic("simulated LLM panic")
		},
	}

	// First failure
	_ = CompactContents(contents, "session-test-fail-1", failMock)
	compactionCircuitBreaker.mu.Lock()
	failures = compactionCircuitBreaker.failures
	isOpen = compactionCircuitBreaker.open
	compactionCircuitBreaker.mu.Unlock()
	if failures != 1 || isOpen {
		t.Errorf("Expected 1 failure, got %d, open=%v", failures, isOpen)
	}

	// Second failure
	_ = CompactContents(contents, "session-test-fail-2", failMock)
	compactionCircuitBreaker.mu.Lock()
	failures = compactionCircuitBreaker.failures
	isOpen = compactionCircuitBreaker.open
	compactionCircuitBreaker.mu.Unlock()
	if failures != 2 || isOpen {
		t.Errorf("Expected 2 failures, got %d, open=%v", failures, isOpen)
	}

	// Third failure -> should trip circuit breaker
	_ = CompactContents(contents, "session-test-fail-3", failMock)
	compactionCircuitBreaker.mu.Lock()
	failures = compactionCircuitBreaker.failures
	isOpen = compactionCircuitBreaker.open
	compactionCircuitBreaker.mu.Unlock()
	if failures != 3 || !isOpen {
		t.Errorf("Expected 3 failures and open circuit breaker, got failures=%d, open=%v", failures, isOpen)
	}

	// 4. Verification that subsequent compaction bypasses LLM and falls back to truncation-only summary
	compactedFallback := CompactContents(contents, "session-test-fallback", mock)
	sysTextFallback := compactedFallback[1].Parts[0].Text
	if !strings.Contains(sysTextFallback, "truncation-only mode") {
		t.Errorf("Expected truncation-only fallback message, got: %q", sysTextFallback)
	}
}

func TestCompaction_StickyLatch(t *testing.T) {
	// Reset circuit breaker state
	compactionCircuitBreaker.mu.Lock()
	compactionCircuitBreaker.failures = 0
	compactionCircuitBreaker.open = false
	compactionCircuitBreaker.mu.Unlock()

	// 1. Create a history of 14 rounds with one sticky block
	contents := make([]*genai.Content, 14)
	for i := 0; i < 14; i++ {
		role := "user"
		if i%2 == 1 {
			role = "model"
		}
		text := fmt.Sprintf("Round message %d", i)
		if i == 5 {
			text = "This is a [STICKY] instruction that must persist"
		}
		contents[i] = &genai.Content{
			Role: role,
			Parts: []*genai.Part{
				{Text: text},
			},
		}
	}

	// 2. Perform compaction
	compacted := CompactContents(contents, "session-test-sticky")
	// Expected: preserved prompt (1), summary (1), sticky (1), and last 4 rounds (4) = 7
	if len(compacted) != 7 {
		t.Fatalf("Expected compacted length of 7, got %d", len(compacted))
	}

	// Check that the sticky block exists in the output
	foundSticky := false
	for _, c := range compacted {
		for _, p := range c.Parts {
			if strings.Contains(p.Text, "[STICKY]") {
				foundSticky = true
			}
		}
	}
	if !foundSticky {
		t.Error("Expected sticky content to be preserved, but it was not found")
	}
}
