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
	Model   string
	BaseURL string
	EnvKey  string
}

// ProviderDefaults maps provider names to their default configuration values
var ProviderDefaults = map[string]ProviderDefaultConfig{
	"glm":    {Model: "glm-4", BaseURL: "https://open.bigmodel.cn/api/paas/v4", EnvKey: "ZHIPU_API_KEY"},
	"openai": {Model: "gpt-4o", BaseURL: "https://api.openai.com/v1", EnvKey: "OPENAI_API_KEY"},
	"claude": {Model: "claude-sonnet-4-6", BaseURL: "https://api.anthropic.com", EnvKey: "ANTHROPIC_API_KEY"},
}

// DefaultProviderConfig returns the ProviderDefaultConfig for a given provider,
// falling back to GLM defaults if the provider is unknown.
func DefaultProviderConfig(provider string) ProviderDefaultConfig {
	if def, ok := ProviderDefaults[provider]; ok {
		return def
	}
	return ProviderDefaults["glm"]
}

// Config holds LLM model and credentials configurations
type Config struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url,omitempty"`
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
				fmt.Printf("  检测到旧版配置文件 %s，正在自动迁移至 %s...\n", oldPath, path)
				if writeErr := os.WriteFile(path, oldData, 0600); writeErr == nil {
					data = oldData
					err = nil
					_ = os.Rename(oldPath, oldPath+".bak")
				} else {
					return nil, writeErr
				}
			} else {
				// Default configuration (simulate mode)
				return &Config{
					Provider: "simulate",
					Model:    ProviderDefaults["glm"].Model,
				}, nil
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
		}
		if cfg.Provider != "" {
			fmt.Printf("  从模型名称 '%s' 推断 provider='%s'。使用 --provider 标志覆盖。\n", cfg.Model, cfg.Provider)
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
		existing = &Config{Provider: "simulate", Model: ProviderDefaults["glm"].Model}
	}

	fmt.Println("\n\x1b[1;32m  Iroha Code CLI 配置向导 (Setup Wizard)\x1b[0m")
	fmt.Println("\x1b[90m  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\x1b[0m")
	fmt.Println("  本向导将指引您配置首选模型、API Key 以及自定义 Base URL。")
	fmt.Printf("  [直接按回车将保留默认值或当前配置]\n\n")

	// 1. Choose Provider
	fmt.Println("  \x1b[36m1. 选择大模型提供商 (LLM Provider):\x1b[0m")
	fmt.Println("     [s] simulate  - 本地高仿真离线沙箱模拟 (免 Key / 无网可用)")
	fmt.Println("     [g] glm       - 智谱 AI GLM-4 官方 API")
	fmt.Println("     [o] openai    - OpenAI 官方或任何兼容第三方 API (DeepSeek/Qwen/Ollama)")
	fmt.Printf("     当前选择: \x1b[33m%s\x1b[0m\n", existing.Provider)
		fmt.Println("     [c] claude    - Anthropic Claude 官方 API")

	fmt.Print("     选择提供商 (s/g/o/c) [回车不修改]: ")
	providerInput, _ := reader.ReadString('\n')
	providerInput = strings.TrimSpace(strings.ToLower(providerInput))

	provider := existing.Provider
	if providerInput == "s" || providerInput == "simulate" {
		provider = "simulate"
	} else if providerInput == "g" || providerInput == "glm" {
		provider = "glm"
	} else if providerInput == "o" || providerInput == "openai" {
		provider = "openai"
	} else if providerInput == "c" || providerInput == "claude" {
		provider = "claude"
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
	fmt.Println("\n  \x1b[36m2. 输入模型名称 (Model Name):\x1b[0m")
	fmt.Printf("     推荐默认值: \x1b[90m%s\x1b[0m\n", defaultModel)
	fmt.Printf("     当前配置: \x1b[33m%s\x1b[0m\n", existing.Model)
	fmt.Printf("     请输入模型名称 [回车保留 %s]: ", existing.Model)
	modelInput, _ := reader.ReadString('\n')
	modelInput = strings.TrimSpace(modelInput)

	model := existing.Model
	if modelInput != "" {
		model = modelInput
	} else if model == "" {
		model = defaultModel
	}

	// 4. Input API Key
	fmt.Println("\n  \x1b[36m3. 输入 API Key (Credentials):\x1b[0m")
	if existing.APIKey != "" {
		masked := existing.APIKey
		if len(masked) > 8 {
			masked = masked[:4] + "...." + masked[len(masked)-4:]
		}
		fmt.Printf("     当前配置: \x1b[33m%s\x1b[0m\n", masked)
	} else {
		fmt.Println("     当前未配置 API Key")
	}
	fmt.Print("     请输入 API Key [直接回车保留原配置]: ")
	apiKeyInput, _ := reader.ReadString('\n')
	apiKeyInput = strings.TrimSpace(apiKeyInput)

	apiKey := existing.APIKey
	if apiKeyInput != "" {
		apiKey = apiKeyInput
	}

	// 5. Input Base URL
	fmt.Println("\n  \x1b[36m4. 输入 API Base URL (自定义端点，留空代表官方端点):\x1b[0m")
	fmt.Printf("     推荐端点: \x1b[90m%s\x1b[0m\n", defaultBaseURL)
	if existing.BaseURL != "" {
		fmt.Printf("     当前配置: \x1b[33m%s\x1b[0m\n", existing.BaseURL)
	} else {
		fmt.Println("     当前配置: 官方默认端点")
	}
	fmt.Print("     请输入 Base URL (或 'default' 重置为官方默认) [回车不修改]: ")
	baseURLInput, _ := reader.ReadString('\n')
	baseURLInput = strings.TrimSpace(baseURLInput)

	baseURL := existing.BaseURL
	if baseURLInput == "default" {
		baseURL = defaultBaseURL
	} else if baseURLInput != "" {
		baseURL = baseURLInput
	} else if baseURL == "" && provider != "simulate" {
		baseURL = defaultBaseURL
	}

	// Save to config
	cfg := &Config{
		Provider: provider,
		Model:    model,
		APIKey:   apiKey,
		BaseURL:  baseURL,
	}

	if err := SaveConfig(cfg); err != nil {
		return nil, fmt.Errorf("保存配置文件失败: %w", err)
	}

	fmt.Println("\n\x1b[1;32m  🎉 配置已成功持久化至 ~/.iroha.json ！\x1b[0m")
	fmt.Printf("\x1b[90m  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\x1b[0m\n\n")

	return cfg, nil
}
