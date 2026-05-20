package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go-claude/pkg/llm"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

func init() {
	llm.NagReminderTrigger = func() string {
		if GlobalTodoManager.RoundsSinceUpdate() >= 3 {
			return "📌 [系统提示] 为了确保后续代码修改的连贯性，请在执行当前步骤前更新您的 todo 计划进度。"
		}
		return ""
	}
	llm.NoteRoundWithoutUpdate = func() {
		GlobalTodoManager.NoteRoundWithoutUpdate()
	}
}

// ConfirmationBridge synchronizes the background agent runner and the foreground TUI
type ConfirmationBridge struct {
	PromptChan   chan string // Agent sends confirmation prompts here
	ResponseChan chan string // TUI sends user responses (y/n/always) here
}

var Bridge = &ConfirmationBridge{
	PromptChan:   make(chan string, 1),
	ResponseChan: make(chan string, 1),
}

// CustomRunner wraps ADK runner and manages background execution
type CustomRunner struct {
	adkRunner *runner.Runner
	llmModel  model.LLM
}

func NewCustomRunner(provider llm.ProviderType, modelName string, apiKey string, baseURL string) (*CustomRunner, error) {
	// 1. Create our abstract model adapter
	modelAdapter, err := llm.NewAdapter(provider, modelName, apiKey, baseURL)
	if err != nil {
		return nil, fmt.Errorf("创建模型适配器失败: %w", err)
	}

	// 2. Load classic SWE tools
	tools, err := GetSWETools()
	if err != nil {
		return nil, fmt.Errorf("加载工具集失败: %w", err)
	}

	// 3. Setup tool with custom confirmation provider that blocks on the Bridge
	wrappedTools := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		// Wrap all tools to run through the permission checking pipeline
		wrappedTools = append(wrappedTools, &blockingConfirmationTool{
			Tool: t,
		})
	}

	// 4. Create llmagent — inject persistent memories into the system instruction
	baseInstruction := "你是一个专业的软件工程助手，名叫 go-claude。你可以帮助用户读取文件、写入文件、在当前工作区运行测试与命令、以及检索代码。对于写文件和运行 Shell 命令等敏感操作，你必须调用相应的工具，并且框架会请求用户确认。请以精美的 Markdown 格式回答用户的问题。"

	// s09: Append any durable memories that survived from previous sessions.
	// "Memory gives direction; current observation gives truth."
	instruction := baseInstruction
	if memSection := GlobalMemoryManager.BuildSystemPromptSection(); memSection != "" {
		instruction = baseInstruction + "\n\n" + memSection
	}

	rootAgent, err := llmagent.New(llmagent.Config{
		Name:        "go-claude-agent",
		Instruction: instruction,
		Model:       modelAdapter,
		Tools:       wrappedTools,
	})

	if err != nil {
		return nil, fmt.Errorf("创建 Agent 失败: %w", err)
	}

	// 5. Create in-memory session service
	sessionService := session.InMemoryService()

	// 6. Create ADK Runner
	adkRunner, err := runner.New(runner.Config{
		AppName:           "go-claude",
		Agent:             rootAgent,
		SessionService:    sessionService,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 Runner 失败: %w", err)
	}

	// 7. Fire SessionStart hooks — runs external scripts once at startup
	GlobalHookManager.RunHooks(HookSessionStart, HookContext{})

	return &CustomRunner{
		adkRunner: adkRunner,
		llmModel:  modelAdapter,
	}, nil
}

func (cr *CustomRunner) ModelName() string {
	if cr.llmModel == nil {
		return "Unknown"
	}
	return cr.llmModel.Name()
}

// Execute handles running a prompt asynchronously and piping events to a callback
func (cr *CustomRunner) Execute(ctx context.Context, userID, sessionID, prompt string, onEvent func(*session.Event), onError func(error), onDone func()) {
	go func() {
		userMsg := &genai.Content{
			Role: "user",
			Parts: []*genai.Part{
				{Text: prompt},
			},
		}

		runConfig := runner.WithStateDelta(nil)
		events := cr.adkRunner.Run(ctx, userID, sessionID, userMsg, agent.RunConfig{
			StreamingMode: agent.StreamingModeSSE,
		}, runConfig)

		for ev, err := range events {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				onError(err)
				return
			}
			if ev != nil {
				onEvent(ev)
			}
		}
		onDone()
	}()
}

// requestProcessor matches the internal interface expected by ADK.
type requestProcessor interface {
	ProcessRequest(ctx tool.Context, req *model.LLMRequest) error
}

// blockingConfirmationTool intercepts execution and blocks on the bridge for y/n response
type adkRunnableTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Run(ctx tool.Context, args any) (map[string]any, error)
}

type blockingConfirmationTool struct {
	tool.Tool
}

// ProcessRequest implements toolinternal.RequestProcessor to forward setup/registration.
func (b *blockingConfirmationTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if rp, ok := b.Tool.(requestProcessor); ok {
		return rp.ProcessRequest(ctx, req)
	}
	return nil
}

func (b *blockingConfirmationTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	// Step 1: Check permissions with the GlobalPermissionManager
	decision, reason := GlobalPermissionManager.Check(b.Name(), args)

	if decision == "deny" {
		return nil, fmt.Errorf("操作被安全策略拒绝: %s", reason)
	}

	runnable, ok := b.Tool.(adkRunnableTool)
	if !ok {
		return nil, fmt.Errorf("tool does not support running: %s", b.Name())
	}

	if decision == "allow" {
		// Silent execution — but still run through hooks
		return b.runWithHooks(ctx, args, runnable)
	}

	// Step 2: "ask" behavior - block on TUI confirmation
	var promptMsg string
	if b.Name() == "shell_run" {
		cmdStr := ""
		if m, ok := args.(map[string]any); ok {
			cmdStr = fmt.Sprintf("%v", m["command"])
		}
		promptMsg = fmt.Sprintf("🔨 \x1b[1;33m[shell_run]\x1b[0m 正在尝试运行命令: \x1b[32m$ %s\x1b[0m\n   ⚠️  %s", cmdStr, reason)
	} else if b.Name() == "file_write" {
		pathStr := ""
		if m, ok := args.(map[string]any); ok {
			pathStr = fmt.Sprintf("%v", m["path"])
		}
		promptMsg = fmt.Sprintf("📝 \x1b[1;36m[file_write]\x1b[0m 正在尝试写入文件: \x1b[32m%s\x1b[0m\n   ⚠️  %s", pathStr, reason)
	} else if b.Name() == "file_read" {
		pathStr := ""
		if m, ok := args.(map[string]any); ok {
			pathStr = fmt.Sprintf("%v", m["path"])
		}
		promptMsg = fmt.Sprintf("📖 \x1b[1;34m[file_read]\x1b[0m 正在尝试读取文件: \x1b[32m%s\x1b[0m\n   ⚠️  %s", pathStr, reason)
	} else if b.Name() == "search_grep" {
		patternStr := ""
		if m, ok := args.(map[string]any); ok {
			patternStr = fmt.Sprintf("%v", m["pattern"])
		}
		promptMsg = fmt.Sprintf("🔍 \x1b[1;35m[search_grep]\x1b[0m 正在尝试全局搜索模式: \x1b[32m\"%s\"\x1b[0m\n   ⚠️  %s", patternStr, reason)
	} else if b.Name() == "todo" {
		promptMsg = fmt.Sprintf("📋 \x1b[1;32m[todo]\x1b[0m 正在尝试更新任务规划进度表\n   ⚠️  %s", reason)
	} else {
		promptMsg = fmt.Sprintf("🔧 \x1b[1;35m[%s]\x1b[0m 正在尝试执行操作: %v\n   ⚠️  %s", b.Name(), args, reason)
	}

	// Send to TUI
	Bridge.PromptChan <- promptMsg

	// Block on response
	approved := <-Bridge.ResponseChan

	if approved == "always" {
		// Dynamically add a temporary session allow rule
		GlobalPermissionManager.AddRule(PermissionRule{
			Tool:     b.Name(),
			Behavior: "allow",
			Path:     "*",
		})
		GlobalPermissionManager.NoteApproval()
		time.Sleep(200 * time.Millisecond) // Smooth animation transition
		return b.runWithHooks(ctx, args, runnable)
	}

	if approved == "y" {
		GlobalPermissionManager.NoteApproval()
		time.Sleep(200 * time.Millisecond) // Smooth animation transition
		return b.runWithHooks(ctx, args, runnable)
	}

	// Any other value or "n" is rejected
	denials := GlobalPermissionManager.NoteDenial()
	warnMsg := ""
	if denials >= 3 {
		warnMsg = fmt.Sprintf("\n⚠️  \x1b[1;33m[安全熔断]\x1b[0m 连续拒绝 %d 次操作。建议您通过输入 `/mode plan` 切换到只读“规划模式”。", denials)
	}
	return nil, fmt.Errorf("操作已被拒绝%s: %w", warnMsg, tool.ErrConfirmationRejected)
}

func (b *blockingConfirmationTool) Declaration() *genai.FunctionDeclaration {
	if b.Name() == "shell_run" || b.Name() == "file_write" {
		decl := &genai.FunctionDeclaration{
			Name: b.Name(),
		}
		if b.Name() == "shell_run" {
			decl.Description = "执行一条 Shell 命令。只允许在当前工作区目录下执行。"
			decl.ParametersJsonSchema = &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"command": {
						Type:        genai.TypeString,
						Description: "要执行的本地 Shell 命令",
					},
				},
				Required: []string{"command"},
			}
		} else if b.Name() == "file_write" {
			decl.Description = "向文件写入指定内容。这会覆盖原文件（如果有的话）。"
			decl.ParametersJsonSchema = &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"path": {
						Type:        genai.TypeString,
						Description: "要写入的文件路径",
					},
					"content": {
						Type:        genai.TypeString,
						Description: "要写入的文本内容",
					},
				},
				Required: []string{"path", "content"},
			}
		}
		return decl
	}
	runnable, ok := b.Tool.(adkRunnableTool)
	if ok {
		return runnable.Declaration()
	}
	return nil
}

// runWithHooks executes a tool through the full s08 hook pipeline:
//
//  1. PreToolUse hooks  — may block (exit 1) or inject a message (exit 2)
//  2. Execute the underlying tool
//  3. PostToolUse hooks — may append annotations to the result (exit 2)
//
// The loop (runner) retains full control of flow; hooks only observe, block,
// or annotate at their named moments.
func (b *blockingConfirmationTool) runWithHooks(ctx tool.Context, args any, runnable adkRunnableTool) (map[string]any, error) {
	hookCtx := HookContext{
		ToolName:  b.Name(),
		ToolInput: args,
	}

	// ── Stage A: PreToolUse ───────────────────────────────────────────────
	preResult := GlobalHookManager.RunHooks(HookPreToolUse, hookCtx)

	if preResult.Blocked {
		return nil, fmt.Errorf("🪝 [Hook 拦截] 工具 %s 被 PreToolUse Hook 阻断: %s", b.Name(), preResult.BlockReason)
	}

	// ── Stage B: Execute the real tool ───────────────────────────────────
	result, err := runnable.Run(ctx, args)
	if err != nil {
		return nil, err
	}

	// ── Stage C: PostToolUse ──────────────────────────────────────────────
	hookCtx.ToolOutput = hookTruncate(fmt.Sprintf("%v", result), 5000)
	postResult := GlobalHookManager.RunHooks(HookPostToolUse, hookCtx)

	// Merge any injected messages from pre + post hooks into the result map
	allMessages := append(preResult.Messages, postResult.Messages...)
	if len(allMessages) > 0 {
		if result == nil {
			result = make(map[string]any)
		}
		result["hook_notes"] = strings.Join(allMessages, "\n")
	}

	return result, nil
}

