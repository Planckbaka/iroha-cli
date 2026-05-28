package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)
func extractStickyBlocks(contents []*genai.Content) []*genai.Content {
	var sticky []*genai.Content
	for _, c := range contents {
		for _, p := range c.Parts {
			if p.Text != "" && strings.Contains(p.Text, stickyMarker) {
				sticky = append(sticky, c)
				break // one match per content block is enough
			}
		}
	}
	return sticky
}

// capStickyContent trims oldest sticky blocks if the total sticky byte count
// exceeds maxStickyFraction of the estimated context window.
func capStickyContent(blocks []*genai.Content) []*genai.Content {
	maxBytes := int(float64(estimatedContextWindowBytes) * maxStickyFraction)

	totalBytes := 0
	for _, b := range blocks {
		for _, p := range b.Parts {
			totalBytes += len(p.Text)
		}
	}

	if totalBytes <= maxBytes {
		return blocks
	}

	// Sort by position (oldest first = lowest index) and trim from the front.
	// blocks are already in chronological order from the conversation.
	sort.SliceStable(blocks, func(i, j int) bool {
		return i < j
	})

	trimmed := make([]*genai.Content, 0, len(blocks))
	// Walk from newest to oldest, keeping as many as fit.
	for i := len(blocks) - 1; i >= 0; i-- {
		blockBytes := 0
		for _, p := range blocks[i].Parts {
			blockBytes += len(p.Text)
		}
		if totalBytes-blockBytes < maxBytes || totalBytes <= maxBytes {
			trimmed = append([]*genai.Content{blocks[i]}, trimmed...)
		} else {
			totalBytes -= blockBytes
		}
	}

	return trimmed
}

// truncateOnlySummary generates a simple truncation-based summary without LLM.
// Used as a fallback when the circuit breaker is active.
func truncateOnlySummary(rounds []*genai.Content) string {
	if len(rounds) == 0 {
		return "[System: No previous conversation history to summarize.]"
	}

	// Extract structured data from rounds
	summary := extractStructuredSummary(rounds)

	var transcriptParts []string
	for _, c := range rounds {
		role := c.Role
		if role == "" || role == "model" {
			role = "assistant"
		}
		for _, p := range c.Parts {
			if p.Text != "" {
				transcriptParts = append(transcriptParts, fmt.Sprintf("%s: %s", role, p.Text))
			}
			if p.FunctionCall != nil {
				transcriptParts = append(transcriptParts, fmt.Sprintf("%s: [Called tool %s]", role, p.FunctionCall.Name))
			}
			if p.FunctionResponse != nil {
				transcriptParts = append(transcriptParts, fmt.Sprintf("tool %s: [responded]", p.FunctionResponse.Name))
			}
		}
	}

	transcript := strings.Join(transcriptParts, "\n")
	// Truncate to keep summary manageable
	if len(transcript) > 4000 {
		transcript = transcript[:4000] + "\n...[truncated]"
	}

	return fmt.Sprintf("%s\n[System: Previous conversational history compacted (truncation-only mode). Summary of completed steps:\n%s]",
		summary, transcript)
}

// extractStructuredSummary scans conversation rounds and extracts tool names,
// file paths, and key decisions into a structured [SUMMARY] block.
func extractStructuredSummary(rounds []*genai.Content) string {
	toolSet := make(map[string]bool)
	fileSet := make(map[string]bool)
	var decisions []string

	// Regex for file paths with extensions (e.g., pkg/agent/tools.go, ./foo/bar.go)
	filePathRe := regexp.MustCompile(`(?:^|[\s"'\(])([\w./\-]+\.[\w]{1,10})(?:[\s"'\):,]|$)`)

	for _, c := range rounds {
		for _, p := range c.Parts {
			// Extract tool call names
			if p.FunctionCall != nil {
				toolSet[p.FunctionCall.Name] = true
			}

			// Extract from text content
			if p.Text != "" {
				// Extract file paths
				matches := filePathRe.FindAllStringSubmatch(p.Text, -1)
				for _, m := range matches {
					if len(m) > 1 && len(m[1]) > 3 {
						fileSet[m[1]] = true
					}
				}

				// Extract key decisions (lines starting with decision phrases)
				lower := strings.ToLower(p.Text)
				lines := strings.Split(p.Text, "\n")
				for _, line := range lines {
					trimmedLine := strings.TrimSpace(line)
					lowerLine := strings.ToLower(trimmedLine)
					if strings.HasPrefix(lowerLine, "let's ") ||
						strings.HasPrefix(lowerLine, "i'll ") ||
						strings.HasPrefix(lowerLine, "we should ") ||
						strings.HasPrefix(lowerLine, "i will ") ||
						strings.HasPrefix(lowerLine, "decided to ") ||
						strings.HasPrefix(lowerLine, "decision:") {
						if len(decisions) < 10 { // cap decisions
							decisions = append(decisions, trimmedLine)
						}
					}
				}

				_ = lower // suppress unused warning
			}

			// Extract file paths from tool arguments
			if p.FunctionCall != nil && p.FunctionCall.Args != nil {
				for _, v := range p.FunctionCall.Args {
					if str, ok := v.(string); ok {
						matches := filePathRe.FindAllStringSubmatch(str, -1)
						for _, m := range matches {
							if len(m) > 1 && len(m[1]) > 3 {
								fileSet[m[1]] = true
							}
						}
					}
				}
			}
		}
	}

	if len(toolSet) == 0 && len(fileSet) == 0 && len(decisions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[SUMMARY]\n")

	if len(toolSet) > 0 {
		tools := sortedKeys(toolSet)
		sb.WriteString(fmt.Sprintf("Tools used: %s\n", strings.Join(tools, ", ")))
	}

	if len(fileSet) > 0 {
		files := sortedKeys(fileSet)
		// Cap at 20 files
		if len(files) > 20 {
			files = files[:20]
		}
		sb.WriteString(fmt.Sprintf("Files: %s\n", strings.Join(files, ", ")))
	}

	if len(decisions) > 0 {
		sb.WriteString(fmt.Sprintf("Decisions: %s\n", strings.Join(decisions, "; ")))
	}

	sb.WriteString("[/SUMMARY]")
	return sb.String()
}

// sortedKeys returns the keys of a map[string]bool in sorted order.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// summarizeRounds generates a compact summary of conversation rounds.
// If an LLM is provided, it uses LLM-based summarization for quality.
// Otherwise falls back to simple text extraction.
func summarizeRounds(rounds []*genai.Content, llm ...model.LLM) string {
	if len(rounds) == 0 {
		return "[System: No previous conversation history to summarize.]"
	}

	// Build structured summary block
	structuredSummary := extractStructuredSummary(rounds)

	// Build transcript from rounds
	var transcriptParts []string
	for _, c := range rounds {
		role := c.Role
		if role == "" || role == "model" {
			role = "assistant"
		}
		for _, p := range c.Parts {
			if p.Text != "" {
				transcriptParts = append(transcriptParts, fmt.Sprintf("%s: %s", role, p.Text))
			}
			if p.FunctionCall != nil {
				transcriptParts = append(transcriptParts, fmt.Sprintf("%s: [Called tool %s]", role, p.FunctionCall.Name))
			}
			if p.FunctionResponse != nil {
				transcriptParts = append(transcriptParts, fmt.Sprintf("tool %s: [responded]", p.FunctionResponse.Name))
			}
		}
	}

	// If no LLM available, fall back to simple extraction
	if len(llm) == 0 || llm[0] == nil {
		result := fmt.Sprintf("[System: Previous conversational history compacted. Summary of completed steps:\n%s]",
			strings.Join(transcriptParts, "\n"))
		if structuredSummary != "" {
			result = structuredSummary + "\n" + result
		}
		return result
	}

	// LLM-based summarization
	transcript := strings.Join(transcriptParts, "\n")
	// Truncate transcript if too large to avoid excessive token cost
	if len(transcript) > 8000 {
		transcript = transcript[:8000] + "\n...[truncated]"
	}

	summarizePrompt := fmt.Sprintf(`Summarize the following conversation transcript into a compact summary that preserves:
1. Key decisions made
2. Constraints established
3. Unresolved issues or open questions
4. Tools used and their outcomes

Be concise but thorough. Do not lose important context.

TRANSCRIPT:
%s`, transcript)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					{Text: summarizePrompt},
				},
			},
		},
	}

	var summaryBuilder strings.Builder
	for resp, err := range llm[0].GenerateContent(ctx, req, false) {
		if err != nil {
			// Fall back to simple extraction on LLM error
			break
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p.Text != "" {
					summaryBuilder.WriteString(p.Text)
				}
			}
		}
		if resp != nil && resp.TurnComplete {
			break
		}
	}

	if summaryBuilder.Len() > 0 {
		result := fmt.Sprintf("[System: Previous conversational history summarized by LLM:\n%s]", summaryBuilder.String())
		if structuredSummary != "" {
			result = structuredSummary + "\n" + result
		}
		return result
	}

	// Fallback to simple extraction if LLM produced nothing
	result := fmt.Sprintf("[System: Previous conversational history compacted. Summary of completed steps:\n%s]",
		strings.Join(transcriptParts, "\n"))
	if structuredSummary != "" {
		result = structuredSummary + "\n" + result
	}
	return result
}
