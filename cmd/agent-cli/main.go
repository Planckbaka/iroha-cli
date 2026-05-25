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
	providerFlag := flag.String("provider", "", "LLM provider: gemini, claude, openai, glm, deepseek, kimi, siliconflow")
	modelFlag := flag.String("model", "", "Model name (e.g. gemini-2.5-flash, glm-4, gpt-4o)")
	apiKeyFlag := flag.String("apikey", "", "LLM API Key")
	baseURLFlag := flag.String("baseurl", "", "Custom API Base URL (e.g. https://api.openai.com/v1)")
	apiFormatFlag := flag.String("api-format", "", "API format: openai (default) or anthropic")
	teammateFlag := flag.String("teammate", "", "Run as a teammate agent (internal mode)")
	socketFlag := flag.String("socket", "", "Unix socket path for IPC")
	forceConfigFlag := flag.Bool("config", false, "Force launch interactive setup wizard")
	resumeFlag := flag.Bool("resume", false, "Open TUI interactive session history picker")
	lastFlag := flag.Bool("last", false, "Auto-resume the most recent active session")
	sessionFlag := flag.String("session", "", "Resume a specific session ID")
	forkFlag := flag.String("fork", "", "Fork a historical session as a new branch")
	yesFlag := flag.Bool("yes", false, "Skip interactive permission confirmation, run in auto mode")
	yShortFlag := flag.Bool("y", false, "Skip interactive permission confirmation, run in auto mode (shorthand)")
	planFlag := flag.Bool("plan", false, "Skip interactive selection, run in plan (read-only) mode")
	pShortFlag := flag.Bool("p", false, "Skip interactive selection, run in plan (read-only) mode (shorthand)")
	defaultFlag := flag.Bool("default", false, "Skip interactive selection, run in default (ask) mode")
	dShortFlag := flag.Bool("d", false, "Skip interactive selection, run in default (ask) mode (shorthand)")
	flag.Parse()

	// Teammate mode: run as a child process connecting to parent via IPC
	if *teammateFlag != "" {
		if *socketFlag == "" {
			fmt.Fprintf(os.Stderr, "\x1b[31m[Error] --socket is required when --teammate is set\x1b[0m\n")
			os.Exit(1)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		fmt.Printf("\x1b[36mStarting in teammate mode: %s (socket: %s)\x1b[0m\n", *teammateFlag, *socketFlag)

		if err := agent.RunTeammateMode(ctx, *teammateFlag, *socketFlag, nil); err != nil {
			fmt.Fprintf(os.Stderr, "\x1b[31m[Teammate error] %v\x1b[0m\n", err)
			os.Exit(1)
		}
		return
	}

	// Track which flags were explicitly set
	setFlags := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = true
	})

	// Load config file
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Printf("\x1b[31m[Config load failed] %v\x1b[0m\n", err)
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
			fmt.Printf("\n\x1b[33mProvider '%s' detected but no API Key provided.\x1b[0m\n", finalProvider)
			fmt.Println("  Launching interactive setup wizard...")
		}
		newCfg, err := config.RunConfigWizard()
		if err != nil {
			fmt.Printf("\x1b[31m[Configuration failed] %v\x1b[0m\n", err)
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
		fmt.Printf("\x1b[31m[Initialization failed] %v\x1b[0m\n", err)
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
			fmt.Printf("Auto-resuming most recent session: %s (title: %s)\n", sessionID, metaList[0].FirstPrompt)
		} else {
			fmt.Println("No active sessions found, starting a new session.")
		}
	} else if *sessionFlag != "" {
		sessionID = *sessionFlag
	} else if *forkFlag != "" {
		originalID := *forkFlag
		newID := uuid.New().String()
		err := agent.GlobalSessionService.ForkSession(context.Background(), originalID, newID)
		if err != nil {
			fmt.Printf("\x1b[31m[Session fork failed] %v\x1b[0m\n", err)
			os.Exit(1)
		}
		sessionID = newID
		fmt.Printf("Forked session %s into new branch: %s\n", originalID, sessionID)
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
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Inject the program reference back into the model via ProgramRef pointer
	m.ProgramRef.P = p

	// 7. Run the TUI Program
	if _, err := p.Run(); err != nil {
		fmt.Printf("\x1b[31m[TUI runtime error] %v\x1b[0m\n", err)
		os.Exit(1)
	}
}
