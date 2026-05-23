package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"iroha/pkg/agent"
	"iroha/pkg/config"
	"iroha/pkg/llm"
	"iroha/pkg/tui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

func main() {
	// 1. Parse command-line flags
	providerFlag := flag.String("provider", "", "LLM 提供商: gemini, claude, openai, glm, deepseek, kimi, siliconflow")
	modelFlag := flag.String("model", "", "模型名称 (如 gemini-2.5-flash, glm-4, gpt-4o)")
	apiKeyFlag := flag.String("apikey", "", "LLM API Key")
	baseURLFlag := flag.String("baseurl", "", "自定义 API Base URL (例如 https://api.openai.com/v1)")
	apiFormatFlag := flag.String("api-format", "", "API 协议格式: openai (默认) 或 anthropic")
	forceConfigFlag := flag.Bool("config", false, "强制启动交互式配置向导")
	resumeFlag := flag.Bool("resume", false, "打开 TUI 交互式历史会话选择器")
	lastFlag := flag.Bool("last", false, "自动恢复最近一次活跃的会话")
	sessionFlag := flag.String("session", "", "恢复指定的会话 ID")
	forkFlag := flag.String("fork", "", "复制指定的历史会话并作为一个新的分支启动")
	yesFlag := flag.Bool("yes", false, "跳过交互式权限确认，直接以 auto 模式运行")
	yShortFlag := flag.Bool("y", false, "跳过交互式权限确认，直接以 auto 模式运行 (简写)")
	planFlag := flag.Bool("plan", false, "跳过交互式选择，直接以 plan (只读) 模式运行")
	pShortFlag := flag.Bool("p", false, "跳过交互式选择，直接以 plan (只读) 模式运行 (简写)")
	defaultFlag := flag.Bool("default", false, "跳过交互式选择，直接以 default (询问) 模式运行")
	dShortFlag := flag.Bool("d", false, "跳过交互式选择，直接以 default (询问) 模式运行 (简写)")
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
	var finalAPIFormat string

	// Provider resolution
	if setFlags["provider"] {
		finalProvider = *providerFlag
	} else if cfg.Provider != "" {
		finalProvider = cfg.Provider
	} else {
		// No provider configured — force config wizard
		*forceConfigFlag = true
	}

	// Model resolution
	if setFlags["model"] {
		finalModel = *modelFlag
	} else if cfg.Model != "" {
		finalModel = cfg.Model
	} else {
		defCfg := config.DefaultProviderConfig(finalProvider)
		finalModel = defCfg.Model
	}

	// BaseURL resolution
	if setFlags["baseurl"] {
		finalBaseURL = *baseURLFlag
	} else if cfg.BaseURL != "" {
		finalBaseURL = cfg.BaseURL
	} else {
		finalBaseURL = config.DefaultProviderConfig(finalProvider).BaseURL
	}

	// APIKey resolution (includes env vars fallback)
	if setFlags["apikey"] {
		finalAPIKey = *apiKeyFlag
	} else {
		defCfg := config.DefaultProviderConfig(finalProvider)
		envKey := os.Getenv(defCfg.EnvKey)

		if envKey != "" {
			finalAPIKey = envKey
		} else if cfg.APIKey != "" {
			finalAPIKey = cfg.APIKey
		}
	}

	// APIFormat resolution
	if setFlags["api-format"] {
		finalAPIFormat = *apiFormatFlag
	} else if cfg.APIFormat != "" {
		finalAPIFormat = cfg.APIFormat
	}

	// 3. Trigger setup wizard if forced or if key is missing
	if *forceConfigFlag || finalAPIKey == "" {
		if finalAPIKey == "" {
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
		finalAPIFormat = newCfg.APIFormat
	}

	// Resolve apiFormat enum
	apiFormat := llm.APIFormatOpenAI
	if finalAPIFormat == "anthropic" {
		apiFormat = llm.APIFormatAnthropic
	}

	fmt.Printf("\x1b[36mInitializing go-claude CLI Agent (Provider: %s, Model: %s, API: %s)...\x1b[0m\n", finalProvider, finalModel, apiFormat)

	// 4. Initialize the agent custom runner
	runner, err := agent.NewCustomRunner(llm.ProviderType(finalProvider), finalModel, finalAPIKey, finalBaseURL, apiFormat)
	if err != nil {
		fmt.Printf("\x1b[31m[初始化失败] %v\x1b[0m\n", err)
		os.Exit(1)
	}

	// 4.5. Resolve Session ID based on CLI flags
	sessionID := ""
	startInSessionPicker := false

	if *resumeFlag {
		startInSessionPicker = true
	} else if *lastFlag {
		metaList, err := agent.GlobalSessionService.ListSavedSessions()
		if err == nil && len(metaList) > 0 {
			sessionID = metaList[0].ID
			fmt.Printf("自动恢复最近一次活跃会话: %s (标题: %s)\n", sessionID, metaList[0].FirstPrompt)
		} else {
			fmt.Println("未找到历史活跃会话，将启动新会话。")
		}
	} else if *sessionFlag != "" {
		sessionID = *sessionFlag
	} else if *forkFlag != "" {
		originalID := *forkFlag
		newID := uuid.New().String()
		err := agent.GlobalSessionService.ForkSession(context.Background(), originalID, newID)
		if err != nil {
			fmt.Printf("\x1b[31m[复制会话失败] %v\x1b[0m\n", err)
			os.Exit(1)
		}
		sessionID = newID
		fmt.Printf("已从会话 %s 复制并创建新分支会话: %s\n", originalID, sessionID)
	}

	if sessionID == "" && !startInSessionPicker {
		sessionID = uuid.New().String()
	}

	// 4.6. Resolve initial permission mode and startup prompt from trailing CLI arguments
	var initialMode agent.PermissionMode
	if *yesFlag || *yShortFlag {
		initialMode = agent.ModeAuto
	} else if *planFlag || *pShortFlag {
		initialMode = agent.ModePlan
	} else if *defaultFlag || *dShortFlag {
		initialMode = agent.ModeDefault
	}

	startupPrompt := strings.Join(flag.Args(), " ")
	if initialMode == "" && startupPrompt != "" {
		initialMode = agent.ModeDefault
	}

	// 5. Create the TUI model
	m := tui.NewModel(runner, sessionID, startInSessionPicker, initialMode, startupPrompt)

	// 6. Create the Bubble Tea Program
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Inject the program reference back into the model via ProgramRef pointer
	m.ProgramRef.P = p

	// 7. Run the TUI Program
	if _, err := p.Run(); err != nil {
		fmt.Printf("\x1b[31m[TUI 运行出错] %v\x1b[0m\n", err)
		os.Exit(1)
	}
}
