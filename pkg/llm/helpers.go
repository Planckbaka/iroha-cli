package llm

import (
	"context"
	"strings"

	"google.golang.org/adk/model"
)

// CollectNonStreaming makes a non-streaming call through model.LLM and collects
// all text parts into a single string. Returns on first error.
func CollectNonStreaming(ctx context.Context, m model.LLM, req *model.LLMRequest) (string, error) {
	var parts []string
	for resp, err := range m.GenerateContent(ctx, req, false) {
		if err != nil {
			return strings.Join(parts, ""), err
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p.Text != "" {
					parts = append(parts, p.Text)
				}
			}
		}
	}
	return strings.Join(parts, ""), nil
}
