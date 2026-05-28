package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// compactionCircuitBreaker tracks consecutive compaction errors and disables
// LLM summarization after 3 failures, falling back to truncation-only mode.
var compactionCircuitBreaker struct {
	mu       sync.Mutex
	failures int
	open     bool
}

// stickyMarker is the sentinel string that marks content blocks as preserved
// during compaction.
const stickyMarker = "[STICKY]"

// maxStickyFraction is the maximum fraction (0.0–1.0) of the context window
// that sticky content may occupy. Oldest sticky blocks are trimmed first when
// the cap is exceeded.
const maxStickyFraction = 0.20

// estimatedContextWindowBytes is a rough estimate of the context window size
// for sticky content cap calculations.
const estimatedContextWindowBytes = 200000

// CompactContents checks for large tool outputs and compacts req.Contents.
// It also compresses older conversation rounds if total rounds > 12.
// The sessionID parameter controls which transcript archive file is used.
// If llm is provided, it uses LLM-based summarization for older rounds;
// otherwise falls back to simple text extraction.
//
// Enhancements:
//   - Sticky latches: content blocks containing [STICKY] are preserved during
//     summarization, capped at 20% of context window.
//   - Circuit breaker: after 3 consecutive compaction errors, LLM summarization
//     is disabled and truncation-only mode is used.
//   - Structured extraction: summaries include extracted tools, files, and decisions.
func CompactContents(contents []*genai.Content, sessionID string, llm ...model.LLM) []*genai.Content {
	if len(contents) == 0 {
		return nil
	}

	// 1. Deep copy contents so we don't modify the session history held in memory.
	copied := make([]*genai.Content, len(contents))
	for i, c := range contents {
		copied[i] = &genai.Content{
			Role: c.Role,
		}
		if c.Parts != nil {
			copied[i].Parts = make([]*genai.Part, len(c.Parts))
			for j, p := range c.Parts {
				var fcCopy *genai.FunctionCall
				if p.FunctionCall != nil {
					fcCopy = &genai.FunctionCall{
						Name: p.FunctionCall.Name,
					}
					if p.FunctionCall.Args != nil {
						argsCopy := make(map[string]any)
						for k, v := range p.FunctionCall.Args {
							argsCopy[k] = v
						}
						fcCopy.Args = argsCopy
					}
				}

				var frCopy *genai.FunctionResponse
				if p.FunctionResponse != nil {
					frCopy = &genai.FunctionResponse{
						Name: p.FunctionResponse.Name,
					}
					if p.FunctionResponse.Response != nil {
						respCopy := make(map[string]any)
						for k, v := range p.FunctionResponse.Response {
							respCopy[k] = v
						}
						frCopy.Response = respCopy
					}
				}

				copied[i].Parts[j] = &genai.Part{
					Text:             p.Text,
					InlineData:       p.InlineData,
					FunctionCall:     fcCopy,
					FunctionResponse: frCopy,
				}
			}
		}
	}

	// 2. Perform Micro-Compaction of large tool outputs (FunctionResponse)
	if sessionID == "" {
		sessionID = "session-default"
	}
	homeDir, _ := os.UserHomeDir()
	archiveDir := filepath.Join(homeDir, ".iroha", "transcripts")
	archivePath := filepath.Join(archiveDir, sessionID+".jsonl")

	// Fire HookCompaction for micro-compaction phase
	GlobalHookManager.RunHooks(HookCompaction, HookContext{
		CompactionType: "micro_compaction",
		SessionID:      sessionID,
	})

	for _, c := range copied {
		for _, p := range c.Parts {
			if p != nil && p.FunctionResponse != nil && p.FunctionResponse.Response != nil {
				respBytes, _ := json.Marshal(p.FunctionResponse.Response)
				respStr := string(respBytes)

				if len(respStr) > 1000 {
					// Archive the full original output to JSONL
					_ = os.MkdirAll(archiveDir, 0755)
					f, err := os.OpenFile(archivePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					if err == nil {
						logEntry := map[string]any{
							"timestamp": time.Now().Format(time.RFC3339),
							"role":      "tool",
							"tool_name": p.FunctionResponse.Name,
							"content":   respStr,
						}
						entryBytes, _ := json.Marshal(logEntry)
						_, _ = f.Write(append(entryBytes, '\n'))
						_ = f.Close()
					}

					// Replace with micro-compaction placeholder
					p.FunctionResponse.Response = map[string]any{
						"output": fmt.Sprintf("[Tool \"%s\" output: %d bytes of output. (Full output archived to %s)]",
							p.FunctionResponse.Name, len(respStr), archivePath),
					}
				}
			}
		}
	}

	// 3. Perform Conversational Summarization if total messages > 12
	if len(copied) > 12 {
		// Fire HookCompaction for summarization phase
		GlobalHookManager.RunHooks(HookCompaction, HookContext{
			CompactionType: "before_summarization",
			SessionID:      sessionID,
		})

		// ── Sticky Latch: extract and preserve [STICKY] content ──────────
		stickyBlocks := extractStickyBlocks(copied)
		stickyBlocks = capStickyContent(stickyBlocks)

		compacted := make([]*genai.Content, 0)

		// Keep the first round (which contains the user's initial prompt)
		compacted = append(compacted, copied[0])

		// Extract middle rounds for summarization (index 1 to len-5)
		midEnd := len(copied) - 4
		if midEnd < 1 {
			midEnd = 1
		}
		middleRounds := copied[1:midEnd]

		var summaryText string
		var compactionErr error

		// Circuit breaker check: if open, use truncation-only mode
		compactionCircuitBreaker.mu.Lock()
		isOpen := compactionCircuitBreaker.open
		failCount := compactionCircuitBreaker.failures
		compactionCircuitBreaker.mu.Unlock()

		if isOpen {
			LogWarn(CatSystem, "compaction_circuit_breaker_active",
				"Compaction circuit breaker active: using truncation-only mode",
				map[string]any{
					"session_id":    sessionID,
					"failure_count": failCount,
				})
			summaryText = truncateOnlySummary(middleRounds)
		} else {
			func() {
				defer func() {
					if r := recover(); r != nil {
						compactionErr = fmt.Errorf("summarization panic: %v", r)
					}
				}()
				summaryText = summarizeRounds(middleRounds, llm...)
			}()
		}

		// Handle compaction error / circuit breaker logic
		if compactionErr != nil || summaryText == "" {
			compactionCircuitBreaker.mu.Lock()
			compactionCircuitBreaker.failures++
			failures := compactionCircuitBreaker.failures
			if failures >= 3 {
				compactionCircuitBreaker.open = true
			}
			compactionCircuitBreaker.mu.Unlock()

			if failures >= 3 {
				LogWarn(CatSystem, "compaction_circuit_breaker_tripped",
					"Compaction circuit breaker tripped: 3 consecutive failures, switching to truncation-only mode",
					map[string]any{
						"session_id":    sessionID,
						"failure_count": failures,
					})
				GlobalHookManager.RunHooks(HookCompaction, HookContext{
					CompactionType: "circuit_breaker_tripped",
					SessionID:      sessionID,
				})
			}
			summaryText = truncateOnlySummary(middleRounds)
		} else {
			compactionCircuitBreaker.mu.Lock()
			compactionCircuitBreaker.failures = 0
			compactionCircuitBreaker.open = false
			compactionCircuitBreaker.mu.Unlock()
		}

		systemSummary := &genai.Content{
			Role: "system",
			Parts: []*genai.Part{
				{Text: summaryText},
			},
		}
		compacted = append(compacted, systemSummary)

		// Re-insert preserved sticky blocks
		compacted = append(compacted, stickyBlocks...)

		// Keep the last 4 rounds
		for i := len(copied) - 4; i < len(copied); i++ {
			compacted = append(compacted, copied[i])
		}

		copied = compacted

		// Fire hook for completion
		GlobalHookManager.RunHooks(HookCompaction, HookContext{
			CompactionType: "after_summarization",
			SessionID:      sessionID,
		})
	}

	return copied
}

// extractStickyBlocks scans contents for [STICKY] markers and returns the
// matching content blocks. These blocks will be re-inserted after summarization.
