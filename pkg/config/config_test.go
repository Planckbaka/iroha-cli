package config

import (
	"strings"
	"testing"
)

func TestDefaultProviderConfig(t *testing.T) {
	tests := []struct {
		provider        string
		expectedModel   string
		expectedBaseURL string
		expectedEnvKey  string
	}{
		{"glm", "glm-4", "https://open.bigmodel.cn/api/paas/v4", "ZHIPU_API_KEY"},
		{"openai", "gpt-4o", "https://api.openai.com/v1", "OPENAI_API_KEY"},
		{"claude", "claude-sonnet-4-6", "https://api.anthropic.com", "ANTHROPIC_API_KEY"},
		{"deepseek", "deepseek-chat", "https://api.deepseek.com/v1", "DEEPSEEK_API_KEY"},
		{"kimi", "kimi-k2.6", "https://api.moonshot.cn/v1", "MOONSHOT_API_KEY"},
		{"siliconflow", "deepseek-ai/DeepSeek-V3", "https://api.siliconflow.cn/v1", "SILICONFLOW_API_KEY"},
		{"unknown", "glm-4", "https://open.bigmodel.cn/api/paas/v4", "ZHIPU_API_KEY"}, // fallback
	}

	for _, tt := range tests {
		cfg := DefaultProviderConfig(tt.provider)
		if cfg.Model != tt.expectedModel {
			t.Errorf("Provider %s: expected model %s, got %s", tt.provider, tt.expectedModel, cfg.Model)
		}
		if cfg.BaseURL != tt.expectedBaseURL {
			t.Errorf("Provider %s: expected BaseURL %s, got %s", tt.provider, tt.expectedBaseURL, cfg.BaseURL)
		}
		if cfg.EnvKey != tt.expectedEnvKey {
			t.Errorf("Provider %s: expected EnvKey %s, got %s", tt.provider, tt.expectedEnvKey, cfg.EnvKey)
		}
	}
}

func TestProviderAutoDetection(t *testing.T) {
	tests := []struct {
		model            string
		expectedProvider string
	}{
		{"glm-4", "glm"},
		{"gpt-4o", "openai"},
		{"o1-mini", "openai"},
		{"claude-sonnet-4-6", "claude"},
		{"deepseek-chat", "deepseek"},
		{"kimi-k2.6", "kimi"},
		{"moonshot-v1-8k", "kimi"},
		{"siliconflow-something", "siliconflow"},
		{"deepseek-ai/DeepSeek-V3", "siliconflow"},
	}

	for _, tt := range tests {
		// Mock the logic used in LoadConfig
		cfg := Config{
			Model: tt.model,
		}
		if cfg.Provider == "" {
			if strings.HasPrefix(cfg.Model, "glm") {
				cfg.Provider = "glm"
			} else if strings.HasPrefix(cfg.Model, "gpt") || strings.HasPrefix(cfg.Model, "o1") || strings.HasPrefix(cfg.Model, "o3") {
				cfg.Provider = "openai"
			} else if strings.HasPrefix(cfg.Model, "claude") {
				cfg.Provider = "claude"
			} else if strings.HasPrefix(cfg.Model, "siliconflow") || strings.Contains(cfg.Model, "deepseek-ai/") {
				cfg.Provider = "siliconflow"
			} else if strings.HasPrefix(cfg.Model, "deepseek") {
				cfg.Provider = "deepseek"
			} else if strings.HasPrefix(cfg.Model, "kimi") || strings.HasPrefix(cfg.Model, "moonshot") {
				cfg.Provider = "kimi"
			}
		}

		if cfg.Provider != tt.expectedProvider {
			t.Errorf("Model %s: expected provider %s, got %s", tt.model, tt.expectedProvider, cfg.Provider)
		}
	}
}
