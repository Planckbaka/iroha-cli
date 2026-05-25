package llm

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// retryBudget tracks session-level retry consumption.
var retryBudget struct {
	mu         sync.Mutex
	used       int
	maxRetries int
}

func init() {
	retryBudget.maxRetries = 10
}

// ConsumeRetry attempts to consume one retry from the session budget.
// Returns false if budget is exhausted.
func ConsumeRetry() bool {
	retryBudget.mu.Lock()
	defer retryBudget.mu.Unlock()
	if retryBudget.used >= retryBudget.maxRetries {
		return false
	}
	retryBudget.used++
	return true
}

// RetryBudgetStatus returns (used, max) for display purposes.
func RetryBudgetStatus() (int, int) {
	retryBudget.mu.Lock()
	defer retryBudget.mu.Unlock()
	return retryBudget.used, retryBudget.maxRetries
}

// ResetRetryBudget resets the session retry counter (e.g., on new session).
func ResetRetryBudget() {
	retryBudget.mu.Lock()
	defer retryBudget.mu.Unlock()
	retryBudget.used = 0
}

// parseRetryAfter extracts a delay in seconds from the Retry-After header.
// Returns 0 if the header is absent or unparseable.
func parseRetryAfter(resp *http.Response) float64 {
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return 0
	}
	// Try integer seconds first.
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return float64(seconds)
	}
	// Try HTTP-date format.
	if t, err := http.ParseTime(retryAfter); err == nil {
		d := time.Until(t).Seconds()
		if d < 1.0 {
			return 1.0
		}
		return d
	}
	return 0
}

// budgetExhaustedError creates a descriptive error for retry budget exhaustion.
func budgetExhaustedError(modelName string, lastErr error) error {
	used, max := RetryBudgetStatus()
	return fmt.Errorf("LLM API (%s): retry budget exhausted (%d/%d retries used this session). Last error: %w", modelName, used, max, lastErr)
}
