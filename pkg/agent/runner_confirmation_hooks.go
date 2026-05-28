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


	"google.golang.org/adk/tool"
)
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
// and blocks further execution after a configurable threshold.
//
// Known limitations:
//   - Exact-argument matching: args are formatted via fmt.Sprintf("%v", args) and compared
//     as strings. Structurally equivalent but differently typed values (e.g. int 0 vs float64 0)
//     may not match, causing the breaker to treat them as separate failure sequences.
//   - No time window: there is no expiry on the failure streak. A tool that failed twice,
//     then succeeded once with different args, then fails once with the original args will
//     reset to 0 on the success and start a new streak — but there is no sliding time window
//     to expire old failures.
//   - Single-instance only: the breaker is global (GlobalToolCircuitBreaker) and not safe
//     for concurrent use across separate agent instances sharing the same process. It resets
//     at the start of each Execute call, which mitigates but does not eliminate cross-turn
//     interference.
//   - No per-tool threshold: all tools share the same failure count threshold (currently 3).
//     A frequently-retried tool with benign failures will trip the breaker at the same rate
//     as a genuinely broken one.
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
