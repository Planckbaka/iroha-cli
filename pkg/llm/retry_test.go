package llm

import (
	"testing"
)

func TestRetryBudget(t *testing.T) {
	ResetRetryBudget()

	used, max := RetryBudgetStatus()
	if used != 0 {
		t.Errorf("expected 0 used, got %d", used)
	}
	if max != 10 {
		t.Errorf("expected max 10, got %d", max)
	}

	// Consume all retries
	for i := range 10 {
		if !ConsumeRetry() {
			t.Errorf("retry %d should succeed", i)
		}
	}

	// Should be exhausted
	if ConsumeRetry() {
		t.Error("expected retry budget to be exhausted")
	}

	// Verify status
	used, _ = RetryBudgetStatus()
	if used != 10 {
		t.Errorf("expected 10 used after exhaustion, got %d", used)
	}

	// Reset and verify
	ResetRetryBudget()
	used, _ = RetryBudgetStatus()
	if used != 0 {
		t.Errorf("expected 0 after reset, got %d", used)
	}

	// Should be able to consume again after reset
	if !ConsumeRetry() {
		t.Error("expected retry to succeed after reset")
	}
}
