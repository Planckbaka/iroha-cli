package llm

import (
	"fmt"
	"strings"

	"github.com/firebase/genkit/go/genkit"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// ProviderType represents the LLM provider
type ProviderType string

const (
	ProviderGemini      ProviderType = "gemini"
	ProviderClaude      ProviderType = "claude"
	ProviderOpenAI      ProviderType = "openai"
	ProviderGLM         ProviderType = "glm"
	ProviderDeepSeek    ProviderType = "deepseek"
	ProviderKimi        ProviderType = "kimi"
	ProviderSiliconFlow ProviderType = "siliconflow"
)

// APIFormat determines which HTTP API protocol to use for a provider.
type APIFormat string

const (
	APIFormatOpenAI    APIFormat = "openai"
	APIFormatAnthropic APIFormat = "anthropic"
)

// AdapterHooks provides runtime callbacks for adapter behavior.
type AdapterHooks interface {
	NagReminder() string
	NoteRound()
	CompactHistory(contents []*genai.Content) []*genai.Content
}

// TokenTracker tracks cumulative token usage across adapter calls.
type TokenTracker interface {
	CumulativeTokens() int
	AddTokens(n int)
}

// NewAdapter creates a new model.LLM based on the provider, model name, apiKey, optional baseURL,
// a systemPrompt string, apiFormat (openai or anthropic), and runtime hooks.
func NewAdapter(g *genkit.Genkit, provider ProviderType, modelName string, apiKey string, baseURL string, systemPrompt string, apiFormat APIFormat, hooks AdapterHooks) (model.LLM, error) {
	// Route to direct adapters based on provider and apiFormat.
	switch provider {
	case ProviderGLM, ProviderOpenAI, ProviderDeepSeek, ProviderKimi, ProviderSiliconFlow:
		if apiFormat == APIFormatAnthropic {
			return NewAnthropicAdapter(modelName, apiKey, baseURL, systemPrompt, hooks), nil
		}
		return NewOpenAICompatibleAdapter(modelName, apiKey, baseURL, systemPrompt, hooks), nil
	case ProviderClaude:
		if g != nil {
			genkitModelName := modelName
			if !strings.HasPrefix(genkitModelName, "anthropic/") {
				genkitModelName = "anthropic/" + genkitModelName
			}
			return NewGenkitModelAdapter(g, genkitModelName, systemPrompt, hooks), nil
		}
		return NewAnthropicAdapter(modelName, apiKey, baseURL, systemPrompt, hooks), nil
	case ProviderGemini:
		if g != nil {
			genkitModelName := modelName
			if !strings.HasPrefix(genkitModelName, "googleai/") {
				genkitModelName = "googleai/" + genkitModelName
			}
			return NewGenkitModelAdapter(g, genkitModelName, systemPrompt, hooks), nil
		}
		return nil, fmt.Errorf("gemini provider requires genkit initialization; ensure GOOGLE_API_KEY is set")
	default:
		return nil, fmt.Errorf("unknown provider: %q", provider)
	}
}
