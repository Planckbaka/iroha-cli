package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"time"

	"iroha/pkg/llm"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

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
	// Let the underlying tool register its function declaration in req.Config.Tools
	if rp, ok := b.Tool.(requestProcessor); ok {
		if err := rp.ProcessRequest(ctx, req); err != nil {
			return err
		}
	}
	// OVERWRITE the entry with our wrapper so the ADK dispatches tool calls
	// through our Run() (which checks permissions) instead of the raw tool.
	// Without this, PackTool stores the unwrapped *functionTool and the
	// confirmation/permission layer is silently bypassed.
	if req.Tools == nil {
		req.Tools = make(map[string]any)
	}
	req.Tools[b.Name()] = b
	return nil
}

func (b *blockingConfirmationTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	agentName := ""
	if ctx != nil {
		if val := ctx.Value(AgentNameKey); val != nil {
			agentName, _ = val.(string)
		}
	}
	prefix := ""
	if agentName != "" {
		prefix = fmt.Sprintf("[%s] ", agentName)
	}

	// Step 1: Check permissions with the GlobalPermissionManager
	decision, reason := GlobalPermissionManager.Check(b.Name(), args)
	LogAudit(CatToolCall, "permission_check", fmt.Sprintf("Tool %s: decision=%s reason=%s", b.Name(), decision, reason), map[string]any{
		"tool":     b.Name(),
		"decision": decision,
		"reason":   reason,
		"args":     args,
	})

	// Record permission decision for trace logging
	toolStartTime := time.Now()

	if decision == "deny" {
		LogToolTrace(b.Name(), args, "denied", time.Since(toolStartTime).Milliseconds())
		denials := GlobalPermissionManager.ConsecutiveDenials()
		warnMsg := ""
		if denials >= 3 {
			warnMsg = fmt.Sprintf("\n⚠️  \x1b[1;33m[Safety Fuse]\x1b[0m %d consecutive denials. Consider switching to read-only Plan mode by typing `/mode plan`.", denials)
		}
		return nil, fmt.Errorf("operation denied by security policy: %s%s", reason, warnMsg)
	}

	runnable, ok := b.Tool.(adkRunnableTool)
	if !ok {
		return nil, fmt.Errorf("tool does not support running: %s", b.Name())
	}

	if decision == "allow" {
		// Silent execution — but still run through hooks
		result, err := b.runWithHooks(ctx, args, runnable)
		status := "success"
		if err != nil {
			status = "error"
		}
		LogToolTrace(b.Name(), args, status, time.Since(toolStartTime).Milliseconds())
		return result, err
	}

	// Step 2: "ask" behavior — first run auto-review, then optionally show human confirmation
	var autoReviewNote string
	if b.Name() == "shell_run" {
		cmdStr := ""
		if m, ok := args.(map[string]any); ok {
			cmdStr = fmt.Sprintf("%v", m["command"])
		}
		// Send to TUI: AI reviewing...
		ToolBridge.Send(ToolStatus{
			Name:    "🤖 ai-review",
			Args:    map[string]any{"command": cmdStr},
			Running: true,
		})

		reviewResult := ReviewCommand(cmdStr)

		// Send review result to TUI
		reviewMsg := fmt.Sprintf("AI review: %s", reviewResult.Reason)
		if reviewResult.Safe {
			reviewMsg = "✅ " + reviewMsg
		} else {
			reviewMsg = "⚠️ " + reviewMsg
		}
		ToolBridge.Send(ToolStatus{
			Name:    "🤖 ai-review",
			Args:    map[string]any{"command": cmdStr},
			Running: false,
			Success: true,
		})

		if reviewResult.Safe && GlobalPermissionManager.GetMode() == ModeAuto {
			// AI says safe — auto-approve silently ONLY in Auto Mode
			GlobalPermissionManager.NoteApproval()
			return b.runWithHooks(ctx, args, runnable)
		}
		// If safe but not in Auto Mode, add an informative note but still prompt for approval
		if reviewResult.Safe {
			autoReviewNote = fmt.Sprintf("\n   [AI Review] %s (will prompt for confirmation under current default mode)", reviewMsg)
		} else {
			autoReviewNote = fmt.Sprintf("\n   [AI Review] %s", reviewMsg)
		}
	} else if b.Name() == "file_write" {
		filePath, fileContent := "", ""
		if m, ok := args.(map[string]any); ok {
			filePath, _ = m["path"].(string)
			fileContent, _ = m["content"].(string)
		}

		ToolBridge.Send(ToolStatus{
			Name:    "ai-review",
			Args:    map[string]any{"file": filePath},
			Running: true,
		})

		reviewResult := ReviewFileOperation("file_write", filePath, fileContent)

		reviewMsg := fmt.Sprintf("AI file review: %s", reviewResult.Reason)
		if reviewResult.Safe {
			reviewMsg = "safe: " + reviewMsg
		} else {
			reviewMsg = "caution: " + reviewMsg
		}
		ToolBridge.Send(ToolStatus{
			Name:    "ai-review",
			Args:    map[string]any{"file": filePath},
			Running: false,
			Success: true,
		})

		if reviewResult.Safe && GlobalPermissionManager.GetMode() == ModeAuto {
			GlobalPermissionManager.NoteApproval()
			return b.runWithHooks(ctx, args, runnable)
		}
		if reviewResult.Safe {
			autoReviewNote = fmt.Sprintf("\n   [AI Review] %s (will prompt for confirmation under current default mode)", reviewMsg)
		} else {
			autoReviewNote = fmt.Sprintf("\n   [AI Review] %s", reviewMsg)
		}
	}

	var promptMsg string
	if b.Name() == "shell_run" {
		cmdStr := ""
		if m, ok := args.(map[string]any); ok {
			cmdStr = fmt.Sprintf("%v", m["command"])
		}
		promptMsg = fmt.Sprintf("%s\x1b[1;33m[shell_run]\x1b[0m attempting to run command: \x1b[32m$ %s\x1b[0m\n   Reason: %s%s", prefix, cmdStr, reason, autoReviewNote)
	} else if b.Name() == "file_write" {
		pathStr := ""
		contentStr := ""
		if m, ok := args.(map[string]any); ok {
			pathStr = fmt.Sprintf("%v", m["path"])
			contentStr, _ = m["content"].(string)
		} else if w, ok := args.(FileWriteArgs); ok {
			pathStr = w.Path
			contentStr = w.Content
		}

		diffStr := ""
		if pathStr != "" {
			diffStr = computeFileDiff(pathStr, contentStr)
		}

		if diffStr != "" {
			promptMsg = fmt.Sprintf("%s\x1b[1;36m[file_write]\x1b[0m attempting to write file: \x1b[32m%s\x1b[0m\n   Reason: %s\n\n\x1b[1;34m[File Changes (Diff)]:\x1b[0m\n%s", prefix, pathStr, reason, diffStr)
		} else {
			promptMsg = fmt.Sprintf("%s\x1b[1;36m[file_write]\x1b[0m attempting to write file: \x1b[32m%s\x1b[0m\n   Reason: %s", prefix, pathStr, reason)
		}
	} else if b.Name() == "file_read" {
		pathStr := ""
		if m, ok := args.(map[string]any); ok {
			pathStr = fmt.Sprintf("%v", m["path"])
		}
		promptMsg = fmt.Sprintf("%s\x1b[1;34m[file_read]\x1b[0m attempting to read file: \x1b[32m%s\x1b[0m\n   Reason: %s", prefix, pathStr, reason)
	} else if b.Name() == "search_grep" {
		patternStr := ""
		if m, ok := args.(map[string]any); ok {
			patternStr = fmt.Sprintf("%v", m["pattern"])
		}
		promptMsg = fmt.Sprintf("%s\x1b[1;35m[search_grep]\x1b[0m attempting to search pattern: \x1b[32m\"%s\"\x1b[0m\n   Reason: %s", prefix, patternStr, reason)
	} else if b.Name() == "todo" {
		promptMsg = fmt.Sprintf("%s\x1b[1;32m[todo]\x1b[0m attempting to update task plan\n   Reason: %s", prefix, reason)
	} else {
		promptMsg = fmt.Sprintf("%s\x1b[1;35m[%s]\x1b[0m attempting to execute: %v\n   Reason: %s", prefix, b.Name(), args, reason)
	}

	var approved string
	for {
		llm.DebugLog("[CONFIRM-TOOL] Sending to PromptChan: tool=%s", b.Name())
		// Send to TUI with cancellation support
		LogAudit(CatToolCall, "confirmation_sent", fmt.Sprintf("Sending confirmation prompt to TUI for tool %s", b.Name()), map[string]any{
			"tool":   b.Name(),
			"prompt": promptMsg,
		})
		select {
		case Bridge.PromptChan <- promptMsg:
		case <-Bridge.CancelChanRead():
			LogToolTrace(b.Name(), args, "blocked", time.Since(toolStartTime).Milliseconds())
			return nil, fmt.Errorf("operation cancelled")
		}

		// Block on response with cancellation support
		select {
		case approved = <-Bridge.ResponseChan:
		case <-Bridge.CancelChanRead():
			LogToolTrace(b.Name(), args, "blocked", time.Since(toolStartTime).Milliseconds())
			return nil, fmt.Errorf("operation cancelled")
		}

		if approved == "explain" {
			// Query the LLM model to explain this tool execution
			var explanation string
			if globalLLMModel != nil {
				explainPrompt := fmt.Sprintf(`The AI Agent is attempting to execute the following tool:
Tool Name: %s
Arguments: %v
Reason: %s

Please explain in 1-2 simple, professional sentences why this tool call is necessary for the current task, and any potential technical or safety implications. Do not use any markdown formatting, prefix, bold text or introductory phrases. Output only the plain explanation text itself.`, b.Name(), args, reason)

				req := &model.LLMRequest{
					Contents: []*genai.Content{
						{
							Role: "user",
							Parts: []*genai.Part{
								{Text: explainPrompt},
							},
						},
					},
				}

				var explainBuilder strings.Builder
				events := globalLLMModel.GenerateContent(ctx, req, false)
				for resp, err := range events {
					if err == nil && resp != nil && resp.Content != nil && len(resp.Content.Parts) > 0 {
						explainBuilder.WriteString(resp.Content.Parts[0].Text)
					}
				}
				explanation = strings.TrimSpace(explainBuilder.String())
			}
			if explanation == "" {
				explanation = fmt.Sprintf("Executing %s is requested to perform current task steps safely under context reasons: %s.", b.Name(), reason)
			}

			// Append explanation to promptMsg and prompt again
			promptMsg = fmt.Sprintf("%s\n\n\x1b[1;32m[AI Explanation]:\x1b[0m\n%s", promptMsg, explanation)
			continue
		}

		if strings.HasPrefix(approved, "edit:") {
			editedVal := strings.TrimPrefix(approved, "edit:")
			// Dynamically update the arguments!
			if m, ok := args.(map[string]any); ok {
				if _, ok := m["command"]; ok {
					m["command"] = editedVal
				} else if _, ok := m["content"]; ok {
					m["content"] = editedVal
				} else if _, ok := m["path"]; ok {
					m["path"] = editedVal
				}
			} else {
				// Handle structured struct
				if w, ok := args.(FileWriteArgs); ok {
					w.Content = editedVal
					args = w
				} else if w, ok := args.(*FileWriteArgs); ok {
					w.Content = editedVal
				}
			}
			approved = "y" // Once edited, auto-approve the edited command/content
		}

		break
	}

	llm.DebugLog("[CONFIRM-TOOL] Executing tool after approval: tool=%s approved=%s", b.Name(), approved)
	if approved == "always" {
		// Dynamically add a temporary session allow rule
		GlobalPermissionManager.AddRule(PermissionRule{
			Tool:     b.Name(),
			Behavior: "allow",
			Path:     "*",
		})
		GlobalPermissionManager.NoteApproval()
		time.Sleep(200 * time.Millisecond) // Smooth animation transition
		result, err := b.runWithHooks(ctx, args, runnable)
		status := "success"
		if err != nil {
			status = "error"
		}
		LogToolTrace(b.Name(), args, status, time.Since(toolStartTime).Milliseconds())
		return result, err
	}

	if approved == "bypass" {
		LogToolTrace(b.Name(), args, "bypassed", time.Since(toolStartTime).Milliseconds())
		return map[string]any{"success": true, "message": "Bypassed by user interactive decision"}, nil
	}

	if approved == "y" {
		GlobalPermissionManager.NoteApproval()
		time.Sleep(200 * time.Millisecond) // Smooth animation transition
		result, err := b.runWithHooks(ctx, args, runnable)
		status := "success"
		if err != nil {
			status = "error"
		}
		LogToolTrace(b.Name(), args, status, time.Since(toolStartTime).Milliseconds())
		return result, err
	}

	// Any other value or "n" is rejected
	denials := GlobalPermissionManager.NoteDenial()
	LogToolTrace(b.Name(), args, "denied", time.Since(toolStartTime).Milliseconds())
	warnMsg := ""
	if denials >= 3 {
		warnMsg = fmt.Sprintf("\n⚠️  \x1b[1;33m[Safety Fuse]\x1b[0m %d consecutive denials. Consider switching to read-only Plan mode by typing `/mode plan`.", denials)
	}
	return nil, fmt.Errorf("operation rejected%s: %w", warnMsg, tool.ErrConfirmationRejected)
}

func (b *blockingConfirmationTool) Declaration() *genai.FunctionDeclaration {
	if b.Name() == "shell_run" || b.Name() == "file_write" {
		decl := &genai.FunctionDeclaration{
			Name: b.Name(),
		}
		if b.Name() == "shell_run" {
			decl.Description = "Execute a Shell command. Only allowed within the current workspace directory."
			decl.ParametersJsonSchema = &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"command": {
						Type:        genai.TypeString,
						Description: "The local Shell command to execute",
					},
				},
				Required: []string{"command"},
			}
		} else if b.Name() == "file_write" {
			decl.Description = "Write specified content to a file. Overwrites the file if it exists."
			decl.ParametersJsonSchema = &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"path": {
						Type:        genai.TypeString,
						Description: "The file path to write to",
					},
					"content": {
						Type:        genai.TypeString,
						Description: "The text content to write",
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
	startTime := time.Now()

	// Send tool start event to the bridge
	ToolBridge.Send(ToolStatus{
		Name:    b.Name(),
		Args:    args,
		Running: true,
	})

	LogInfo(CatToolCall, "tool_start", fmt.Sprintf("Starting tool execution: %s", b.Name()), map[string]any{
		"tool": b.Name(),
		"args": args,
	})

	hookCtx := HookContext{
		ToolName:  b.Name(),
		ToolInput: args,
	}

	// ── Stage A: PreToolUse ───────────────────────────────────────────────
	preResult := GlobalHookManager.RunHooks(HookPreToolUse, hookCtx)

	if preResult.Blocked {
		err := fmt.Errorf("🪝 [Hook Block] Tool %s blocked by PreToolUse hook: %s", b.Name(), preResult.BlockReason)
		durationMS := time.Since(startTime).Milliseconds()
		LogAudit(CatToolCall, "tool_hook_blocked", fmt.Sprintf("Tool %s blocked by PreToolUse hook", b.Name()), map[string]any{
			"tool":        b.Name(),
			"reason":      preResult.BlockReason,
			"duration_ms": durationMS,
		})
		ToolBridge.Send(ToolStatus{
			Name:     b.Name(),
			Args:     args,
			Running:  false,
			Success:  false,
			Error:    err,
			Duration: time.Since(startTime),
		})
		return nil, err
	}

	// Dynamic tool input rewrite from PreToolUse hook
	if preResult.UpdatedInput != nil {
		marshaled, err := json.Marshal(preResult.UpdatedInput)
		if err == nil {
			newArgsPtr := reflect.New(reflect.TypeOf(args))
			if err := json.Unmarshal(marshaled, newArgsPtr.Interface()); err == nil {
				args = newArgsPtr.Elem().Interface()
				hookCtx.ToolInput = args
				LogInfo(CatToolCall, "tool_args_rewritten", fmt.Sprintf("Tool %s arguments rewritten by PreToolUse hook", b.Name()), map[string]any{
					"updated_args": args,
				})
			}
		}
	}

	// ── Stage B: Execute the real tool ───────────────────────────────────
	result, err := runnable.Run(ctx, args)
	durationMS := time.Since(startTime).Milliseconds()

	// Circuit breaker check
	isFailure := err != nil
	if !isFailure && result != nil && b.Name() == "shell_run" {
		if ec, ok := result["exit_code"]; ok {
			if ecInt, ok := ec.(int); ok && ecInt != 0 {
				isFailure = true
			} else if ecFloat, ok := ec.(float64); ok && ecFloat != 0 {
				isFailure = true
			}
		}
	}

	count := GlobalToolCircuitBreaker.Track(b.Name(), args, isFailure)
	if isFailure && count >= 3 {
		err = fmt.Errorf("[Circuit Breaker] Tool %s has failed %d consecutive times with identical arguments. To prevent infinite loops and excessive token usage, this tool has been circuit-broken. Stop retrying this tool, report the issue to the user and seek human guidance.", b.Name(), count)
		LogAudit(CatToolCall, "tool_circuit_breaker_blocked", fmt.Sprintf("Tool %s blocked by circuit breaker", b.Name()), map[string]any{
			"tool":        b.Name(),
			"args":        args,
			"failures":    count,
			"duration_ms": durationMS,
		})
		ToolBridge.Send(ToolStatus{
			Name:     b.Name(),
			Args:     args,
			Running:  false,
			Success:  false,
			Error:    err,
			Duration: time.Since(startTime),
		})
		return nil, err
	}

	if err != nil {
		LogError(CatToolCall, "tool_failed", fmt.Sprintf("Tool %s execution failed after %dms", b.Name(), durationMS), err, map[string]any{
			"tool":        b.Name(),
			"args":        args,
			"duration_ms": durationMS,
		})
		// Fire HookToolError when a tool execution fails
		GlobalHookManager.RunHooks(HookToolError, HookContext{
			ToolName:  b.Name(),
			ToolInput: args,
			ToolError: err.Error(),
		})
		ToolBridge.Send(ToolStatus{
			Name:     b.Name(),
			Args:     args,
			Running:  false,
			Success:  false,
			Error:    err,
			Duration: time.Since(startTime),
		})
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

	// Merge injected conversation contexts from pre + post hooks
	var contextParts []string
	if preResult.AdditionalContext != "" {
		contextParts = append(contextParts, preResult.AdditionalContext)
	}
	if postResult.AdditionalContext != "" {
		contextParts = append(contextParts, postResult.AdditionalContext)
	}

	// Self-Healing post-edit compile verification check
	if b.Name() == "file_edit" || b.Name() == "file_write" || b.Name() == "file_edit_batch" {
		cmd := exec.Command("go", "build", "-o", os.DevNull, "./pkg/agent/...")
		cmd.Dir = findGoModuleRoot()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			compileErr := stderr.String()
			if compileErr != "" {
				contextParts = append(contextParts, fmt.Sprintf("⚠️ [Post-Edit Compiler Alert] Your recent edit introduced compile errors. Please prioritize fixing the compilation failure immediately before proceeding:\n%s", compileErr))
			}
		}
	}

	if len(contextParts) > 0 {
		if result == nil {
			result = make(map[string]any)
		}
		result["additional_context"] = strings.Join(contextParts, "\n")
	}

	// Send tool end event to the bridge
	ToolBridge.Send(ToolStatus{
		Name:     b.Name(),
		Args:     args,
		Running:  false,
		Success:  true,
		Duration: time.Since(startTime),
	})

	GlobalLogger.Log(LevelInfo, CatToolCall, "tool_success", fmt.Sprintf("Tool %s completed successfully", b.Name()), durationMS, map[string]any{
		"tool":        b.Name(),
		"duration_ms": durationMS,
		"result_keys": func() []string {
			var keys []string
			for k := range result {
				keys = append(keys, k)
			}
			return keys
		}(),
		"has_hook_notes": len(allMessages) > 0,
	})

	return result, nil
}

// ToolCircuitBreaker tracks consecutive failures of the same tool with the same arguments
type ToolCircuitBreaker struct {
	mu           sync.Mutex
	lastTool     string
	lastArgsStr  string
	failureCount int
}

func (cb *ToolCircuitBreaker) Track(toolName string, args any, isFailure bool) int {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	argsStr := fmt.Sprintf("%v", args)
	if isFailure {
		if cb.lastTool == toolName && cb.lastArgsStr == argsStr {
			cb.failureCount++
		} else {
			cb.lastTool = toolName
			cb.lastArgsStr = argsStr
			cb.failureCount = 1
		}
		return cb.failureCount
	} else {
		if cb.lastTool == toolName && cb.lastArgsStr == argsStr {
			cb.failureCount = 0
		}
		return 0
	}
}

func (cb *ToolCircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.lastTool = ""
	cb.lastArgsStr = ""
	cb.failureCount = 0
}

var GlobalToolCircuitBreaker = &ToolCircuitBreaker{}
