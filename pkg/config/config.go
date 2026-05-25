package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProviderDefaultConfig holds per-provider default values
type ProviderDefaultConfig struct {
	Model            string
	BaseURL          string
	AnthropicBaseURL string // Anthropic-compatible API endpoint (empty if not supported)
	EnvKey           string
}

// ProviderDefaults maps provider names to their default configuration values
var ProviderDefaults = map[string]ProviderDefaultConfig{
	"glm":         {Model: "glm-4", BaseURL: "https://open.bigmodel.cn/api/paas/v4", AnthropicBaseURL: "https://open.bigmodel.cn/api/anthropic", EnvKey: "ZHIPU_API_KEY"},
	"openai":      {Model: "gpt-4o", BaseURL: "https://api.openai.com/v1", EnvKey: "OPENAI_API_KEY"},
	"claude":      {Model: "claude-sonnet-4-6", BaseURL: "https://api.anthropic.com", EnvKey: "ANTHROPIC_API_KEY"},
	"deepseek":    {Model: "deepseek-chat", BaseURL: "https://api.deepseek.com/v1", AnthropicBaseURL: "https://api.deepseek.com/anthropic", EnvKey: "DEEPSEEK_API_KEY"},
	"kimi":        {Model: "kimi-k2.6", BaseURL: "https://api.moonshot.cn/v1", EnvKey: "MOONSHOT_API_KEY"},
	"siliconflow": {Model: "deepseek-ai/DeepSeek-V3", BaseURL: "https://api.siliconflow.cn/v1", EnvKey: "SILICONFLOW_API_KEY"},
}

// DefaultProviderConfig returns the ProviderDefaultConfig for a given provider,
// falling back to GLM defaults if the provider is unknown.
func DefaultProviderConfig(provider string) ProviderDefaultConfig {
	if def, ok := ProviderDefaults[provider]; ok {
		return def
	}
	return ProviderDefaults["glm"]
}

// LSPServerConfig defines a language server for a specific language.
type LSPServerConfig struct {
	Language     string   `json:"language"`
	Command      string   `json:"command"`
	Args         []string `json:"args,omitempty"`
	FilePatterns []string `json:"file_patterns,omitempty"`
}

// Config holds LLM model and credentials configurations
type Config struct {
	Provider   string            `json:"provider"`
	Model      string            `json:"model"`
	APIKey     string            `json:"api_key"`
	BaseURL    string            `json:"base_url,omitempty"`
	APIFormat  string            `json:"api_format,omitempty"` // "openai" (default) or "anthropic"
	LSPServers []LSPServerConfig `json:"lsp_servers,omitempty"`
}

// GetConfigPath returns the absolute path to user configuration file (~/.iroha.json)
func GetConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".iroha.json")
}

// LoadConfig loads or initializes configuration from ~/.iroha.json
func LoadConfig() (*Config, error) {
	path := GetConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Check if old config file (~/.go-claude.json) exists for backward compatibility and auto-migrate
			home, _ := os.UserHomeDir()
			oldPath := filepath.Join(home, ".go-claude.json")
			if oldData, oldErr := os.ReadFile(oldPath); oldErr == nil {
				fmt.Printf("  Detected legacy config file %s, auto-migrating to %s...\n", oldPath, path)
				if writeErr := os.WriteFile(path, oldData, 0600); writeErr == nil {
					data = oldData
					err = nil
					_ = os.Rename(oldPath, oldPath+".bak")
				} else {
					return nil, writeErr
				}
			} else {
				return nil, fmt.Errorf("no configuration file found at %s; run with --config flag to set up a provider", path)
			}
		} else {
			return nil, err
		}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Auto-detect provider from model name for backward compatibility
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
		if cfg.Provider != "" {
			fmt.Printf("  Inferred provider='%s' from model name '%s'. Use --provider flag to override.\n", cfg.Model, cfg.Provider)
		}
	}

	return &cfg, nil
}

// SaveConfig persists the configurations to ~/.iroha.json
func SaveConfig(cfg *Config) error {
	path := GetConfigPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// RunConfigWizard triggers an interactive step-by-step configuration terminal wizard
func RunConfigWizard() (*Config, error) {
	reader := bufio.NewReader(os.Stdin)

	// Load existing config to serve as defaults
	existing, _ := LoadConfig()
	if existing == nil {
		existing = &Config{Provider: "glm", Model: ProviderDefaults["glm"].Model}
	}

	fmt.Println("\n\x1b[1;32m  Iroha Code CLI Setup Wizard\x1b[0m")
	fmt.Println("\x1b[90m  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\x1b[0m")
	fmt.Println("  This wizard will guide you through configuring your preferred model, API Key, and custom Base URL.")
	fmt.Printf("  [Press Enter to keep default or current value]\n\n")

	// 1. Choose Provider
	fmt.Println("  \x1b[36m1. Select LLM Provider:\x1b[0m")
	fmt.Println("     [g] glm         - Zhipu AI GLM-4 Official API")
	fmt.Println("     [o] openai      - OpenAI official or any compatible third-party API (Ollama/local models)")
	fmt.Println("     [c] claude      - Anthropic Claude Official API")
	fmt.Println("     [d] deepseek    - DeepSeek Official API")
	fmt.Println("     [k] kimi        - Moonshot Kimi Official API")
	fmt.Println("     [f] siliconflow - SiliconFlow API (fast deployment of DeepSeek V3/R1)")
	fmt.Printf("     Current selection: \x1b[33m%s\x1b[0m\n", existing.Provider)
	fmt.Print("     Select provider (g/o/c/d/k/f) [Enter to skip]: ")
	providerInput, _ := reader.ReadString('\n')
	providerInput = strings.TrimSpace(strings.ToLower(providerInput))

	provider := existing.Provider
	if providerInput == "g" || providerInput == "glm" {
		provider = "glm"
	} else if providerInput == "o" || providerInput == "openai" {
		provider = "openai"
	} else if providerInput == "c" || providerInput == "claude" {
		provider = "claude"
	} else if providerInput == "d" || providerInput == "deepseek" {
		provider = "deepseek"
	} else if providerInput == "k" || providerInput == "kimi" {
		provider = "kimi"
	} else if providerInput == "f" || providerInput == "siliconflow" {
		provider = "siliconflow"
	}

	// 2. Determine default model and base URL based on provider
	defCfg := DefaultProviderConfig(provider)
	defaultModel := defCfg.Model
	defaultBaseURL := defCfg.BaseURL

	if existing.Provider == provider {
		defaultModel = existing.Model
		defaultBaseURL = existing.BaseURL
	}

	// 3. Choose Model Name
	fmt.Println("\n  \x1b[36m2. Enter Model Name:\x1b[0m")
	fmt.Printf("     Recommended default: \x1b[90m%s\x1b[0m\n", defaultModel)
	fmt.Printf("     Current config: \x1b[33m%s\x1b[0m\n", existing.Model)
	fmt.Printf("     Enter model name [Enter to keep %s]: ", existing.Model)
	modelInput, _ := reader.ReadString('\n')
	modelInput = strings.TrimSpace(modelInput)

	model := existing.Model
	if modelInput != "" {
		model = modelInput
	} else if model == "" {
		model = defaultModel
	}

	// 4. Input API Key
	fmt.Println("\n  \x1b[36m3. Enter API Key (Credentials):\x1b[0m")
	if existing.APIKey != "" {
		masked := existing.APIKey
		if len(masked) > 8 {
			masked = masked[:4] + "...." + masked[len(masked)-4:]
		}
		fmt.Printf("     Current config: \x1b[33m%s\x1b[0m\n", masked)
	} else {
		fmt.Println("     No API Key configured")
	}
	fmt.Print("     Enter API Key [Enter to keep current]: ")
	apiKeyInput, _ := reader.ReadString('\n')
	apiKeyInput = strings.TrimSpace(apiKeyInput)

	apiKey := existing.APIKey
	if apiKeyInput != "" {
		apiKey = apiKeyInput
	}

	// 5. Input Base URL
	defCfgForURL := DefaultProviderConfig(provider)
	fmt.Println("\n  \x1b[36m4. Enter API Base URL (custom endpoint, leave empty for official):\x1b[0m")
	fmt.Printf("     OpenAI endpoint:      \x1b[90m%s\x1b[0m\n", defCfgForURL.BaseURL)
	if defCfgForURL.AnthropicBaseURL != "" {
		fmt.Printf("     Anthropic endpoint:   \x1b[90m%s\x1b[0m\n", defCfgForURL.AnthropicBaseURL)
	}
	if existing.BaseURL != "" {
		formatHint := "OpenAI"
		if existing.APIFormat == "anthropic" {
			formatHint = "Anthropic"
		}
		fmt.Printf("     Current config: \x1b[33m%s\x1b[0m (\x1b[90m%s format\x1b[0m)\n", existing.BaseURL, formatHint)
	} else {
		fmt.Println("     Current config: Official default endpoint")
	}
	fmt.Println("     Note: URL must match the selected API protocol format (step 5 to switch)")
	fmt.Print("     Enter Base URL (or 'default' to reset) [Enter to skip]: ")
	baseURLInput, _ := reader.ReadString('\n')
	baseURLInput = strings.TrimSpace(baseURLInput)

	baseURL := existing.BaseURL
	if baseURLInput == "default" {
		baseURL = defaultBaseURL
	} else if baseURLInput != "" {
		baseURL = baseURLInput
	} else if baseURL == "" {
		baseURL = defaultBaseURL
	}

	// 6. API Format (for providers that support multiple API protocols)
	apiFormat := existing.APIFormat
	supportsAnthropic := defCfgForURL.AnthropicBaseURL != ""
	if supportsAnthropic {
		fmt.Println("\n  \x1b[36m5. Select API Format:\x1b[0m")
		fmt.Printf("     [o] openai     - OpenAI Chat Completions format (endpoint: %s)\n", defCfgForURL.BaseURL)
		fmt.Printf("     [a] anthropic  - Anthropic Messages format (endpoint: %s)\n", defCfgForURL.AnthropicBaseURL)
		if apiFormat == "anthropic" {
			fmt.Printf("     Current config: \x1b[33manthropic\x1b[0m\n")
		} else {
			fmt.Printf("     Current config: \x1b[33mopenai (default)\x1b[0m\n")
		}
		fmt.Print("     Select format (o/a) [Enter to skip]: ")
		formatInput, _ := reader.ReadString('\n')
		formatInput = strings.TrimSpace(strings.ToLower(formatInput))
		if formatInput == "a" || formatInput == "anthropic" {
			apiFormat = "anthropic"
			// Auto-suggest Anthropic-compatible base URL
			if baseURL != defCfgForURL.AnthropicBaseURL {
				fmt.Printf("     \x1b[33mNote: Recommended Anthropic endpoint is %s, current is %s\x1b[0m\n", defCfgForURL.AnthropicBaseURL, baseURL)
				fmt.Print("     Auto-switch to recommended endpoint? (y/n) [y]: ")
				switchInput, _ := reader.ReadString('\n')
				switchInput = strings.TrimSpace(strings.ToLower(switchInput))
				if switchInput == "" || switchInput == "y" || switchInput == "yes" {
					baseURL = defCfgForURL.AnthropicBaseURL
				}
			}
		} else if formatInput == "o" || formatInput == "openai" {
			apiFormat = "openai"
		}
	} else {
		apiFormat = ""
	}

	// Save to config
	cfg := &Config{
		Provider:  provider,
		Model:     model,
		APIKey:    apiKey,
		BaseURL:   baseURL,
		APIFormat: apiFormat,
	}

	if err := SaveConfig(cfg); err != nil {
		return nil, fmt.Errorf("failed to save config file: %w", err)
	}

	fmt.Println("\n\x1b[1;32m  Configuration saved successfully to ~/.iroha.json!\x1b[0m")
	fmt.Printf("\x1b[90m  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\x1b[0m\n\n")

	return cfg, nil
}

// ModelPricing holds the input and output token costs per million tokens in USD
type ModelPricing struct {
	InputCostPerMillion  float64
	OutputCostPerMillion float64
}

// ModelPricingMap maps model identifier substrings to their Input/Output pricing per million tokens in USD.
var ModelPricingMap = map[string]ModelPricing{
	"claude-3-5-sonnet": {InputCostPerMillion: 3.00, OutputCostPerMillion: 15.00},
	"claude-sonnet":     {InputCostPerMillion: 3.00, OutputCostPerMillion: 15.00},
	"claude-3-5-haiku":  {InputCostPerMillion: 0.80, OutputCostPerMillion: 4.00},
	"claude-3-haiku":    {InputCostPerMillion: 0.25, OutputCostPerMillion: 1.25},
	"claude-3-opus":     {InputCostPerMillion: 15.00, OutputCostPerMillion: 75.00},
	"gpt-4o-mini":       {InputCostPerMillion: 0.15, OutputCostPerMillion: 0.60},
	"gpt-4o":            {InputCostPerMillion: 2.50, OutputCostPerMillion: 10.00},
	"o1-mini":           {InputCostPerMillion: 3.00, OutputCostPerMillion: 12.00},
	"o1":                {InputCostPerMillion: 15.00, OutputCostPerMillion: 60.00},
	"o3-mini":           {InputCostPerMillion: 1.10, OutputCostPerMillion: 4.40},
	"deepseek-chat":     {InputCostPerMillion: 0.14, OutputCostPerMillion: 0.28},
	"deepseek-v3":       {InputCostPerMillion: 0.14, OutputCostPerMillion: 0.28},
	"deepseek-r1":       {InputCostPerMillion: 0.55, OutputCostPerMillion: 2.19},
	"glm-4-flash":       {InputCostPerMillion: 0.00, OutputCostPerMillion: 0.00},
	"glm-4":             {InputCostPerMillion: 0.10, OutputCostPerMillion: 0.10},
	"kimi":              {InputCostPerMillion: 1.00, OutputCostPerMillion: 1.00},
	"moonshot":          {InputCostPerMillion: 1.00, OutputCostPerMillion: 1.00},
}

// EstimateCost estimates session cost in USD based on model name and total token count.
// Uses fuzzy model name normalization and a realistic 85%/15% input/output token ratio.
func EstimateCost(modelName string, totalTokens int) float64 {
	if totalTokens <= 0 {
		return 0.0
	}
	modelName = strings.ToLower(modelName)
	pricing := ModelPricing{InputCostPerMillion: 1.50, OutputCostPerMillion: 6.00} // Default fallback pricing
	found := false

	// Fuzzy match
	for k, p := range ModelPricingMap {
		if strings.Contains(modelName, k) {
			pricing = p
			found = true
			break
		}
	}

	// Try provider heuristic if no direct match
	if !found {
		if strings.Contains(modelName, "gpt") || strings.Contains(modelName, "openai") {
			pricing = ModelPricingMap["gpt-4o"]
		} else if strings.Contains(modelName, "claude") {
			pricing = ModelPricingMap["claude-sonnet"]
		} else if strings.Contains(modelName, "deepseek") {
			pricing = ModelPricingMap["deepseek-chat"]
		} else if strings.Contains(modelName, "glm") || strings.Contains(modelName, "zhipu") {
			pricing = ModelPricingMap["glm-4"]
		} else if strings.Contains(modelName, "kimi") || strings.Contains(modelName, "moonshot") {
			pricing = ModelPricingMap["kimi"]
		}
	}

	inputTokens := 0.85 * float64(totalTokens)
	outputTokens := 0.15 * float64(totalTokens)

	inputCost := (inputTokens / 1000000.0) * pricing.InputCostPerMillion
	outputCost := (outputTokens / 1000000.0) * pricing.OutputCostPerMillion

	return inputCost + outputCost
}
