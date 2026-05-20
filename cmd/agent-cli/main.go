package main

import (
	"flag"
	"fmt"
	"os"

	"go-claude/pkg/agent"
	"go-claude/pkg/config"
	"go-claude/pkg/llm"
	"go-claude/pkg/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// 1. Parse command-line flags
	providerFlag := flag.String("provider", "", "LLM 提供商: simulate, gemini, claude, openai, glm")
	modelFlag := flag.String("model", "", "模型名称 (如 gemini-2.5-flash, glm-4, gpt-4o)")
	apiKeyFlag := flag.String("apikey", "", "LLM API Key")
	baseURLFlag := flag.String("baseurl", "", "自定义 API Base URL (例如 https://api.openai.com/v1)")
	forceConfigFlag := flag.Bool("config", false, "强制启动交互式配置向导")
	flag.Parse()

	// Track which flags were explicitly set
	setFlags := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = true
	})

	// Load config file
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Printf("\x1b[31m[加载配置失败] %v\x1b[0m\n", err)
		os.Exit(1)
	}

	// 2. Resolve final values using the priority override hierarchy
	var finalProvider string
	var finalModel string
	var finalAPIKey string
	var finalBaseURL string

	// Provider resolution
	if setFlags["provider"] {
		finalProvider = *providerFlag
	} else if cfg.Provider != "" {
		finalProvider = cfg.Provider
	} else {
		finalProvider = "simulate"
	}

	// Model resolution
	if setFlags["model"] {
		finalModel = *modelFlag
	} else if cfg.Model != "" {
		finalModel = cfg.Model
	} else {
		if finalProvider == "openai" {
			finalModel = "gpt-4o"
		} else {
			finalModel = "glm-4"
		}
	}

	// BaseURL resolution
	if setFlags["baseurl"] {
		finalBaseURL = *baseURLFlag
	} else if cfg.BaseURL != "" {
		finalBaseURL = cfg.BaseURL
	} else {
		if finalProvider == "glm" {
			finalBaseURL = "https://open.bigmodel.cn/api/paas/v4"
		} else if finalProvider == "openai" {
			finalBaseURL = "https://api.openai.com/v1"
		}
	}

	// APIKey resolution (includes env vars fallback)
	if setFlags["apikey"] {
		finalAPIKey = *apiKeyFlag
	} else {
		var envKey string
		switch finalProvider {
		case "gemini":
			envKey = os.Getenv("GEMINI_API_KEY")
		case "claude":
			envKey = os.Getenv("ANTHROPIC_API_KEY")
		case "openai":
			envKey = os.Getenv("OPENAI_API_KEY")
		case "glm":
			envKey = os.Getenv("ZHIPU_API_KEY")
		}

		if envKey != "" {
			finalAPIKey = envKey
		} else if cfg.APIKey != "" {
			finalAPIKey = cfg.APIKey
		}
	}

	// 3. Trigger setup wizard if forced or if key is missing for online provider
	if *forceConfigFlag || (finalProvider != "simulate" && finalAPIKey == "") {
		if finalProvider != "simulate" && finalAPIKey == "" {
			fmt.Printf("\n\x1b[33m⚠️ 检测到提供商为 '%s' 但未提供 API Key。\x1b[0m\n", finalProvider)
			fmt.Println("  将自动为您启动交互式配置向导...")
		}
		newCfg, err := config.RunConfigWizard()
		if err != nil {
			fmt.Printf("\x1b[31m[配置失败] %v\x1b[0m\n", err)
			os.Exit(1)
		}
		finalProvider = newCfg.Provider
		finalModel = newCfg.Model
		finalAPIKey = newCfg.APIKey
		finalBaseURL = newCfg.BaseURL
	}

	fmt.Printf("\x1b[36mInitializing go-claude CLI Agent (Provider: %s, Model: %s)...\x1b[0m\n", finalProvider, finalModel)

	// 4. Initialize the agent custom runner
	runner, err := agent.NewCustomRunner(llm.ProviderType(finalProvider), finalModel, finalAPIKey, finalBaseURL)
	if err != nil {
		fmt.Printf("\x1b[31m[初始化失败] %v\x1b[0m\n", err)
		os.Exit(1)
	}

	// 5. Create the TUI model
	m := tui.NewModel(runner)

	// 6. Create the Bubble Tea Program
	p := tea.NewProgram(m)
	
	// Inject the program reference back into the model via ProgramRef pointer
	m.ProgramRef.P = p

	// 7. Run the TUI Program
	if _, err := p.Run(); err != nil {
		fmt.Printf("\x1b[31m[TUI 运行出错] %v\x1b[0m\n", err)
		os.Exit(1)
	}
}
