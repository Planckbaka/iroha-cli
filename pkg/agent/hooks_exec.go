package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)
func (hm *HookManager) RunHooks(event HookEvent, ctx HookContext) HookResult {
	hm.mu.RLock()
	defs := append([]HookDef{}, hm.hooks[string(event)]...)
	hm.mu.RUnlock()

	result := HookResult{}
	for _, def := range defs {
		// Apply optional tool-name matcher
		if def.Matcher != "" && def.Matcher != "*" && def.Matcher != ctx.ToolName {
			continue
		}

		if def.Async {
			go func(d HookDef, c HookContext) {
				_ = hm.runOne(event, d, c)
			}(def, ctx)
			continue
		}

		hr := hm.runOne(event, def, ctx)
		if hr.Blocked {
			return hr
		}
		result.Messages = append(result.Messages, hr.Messages...)
		if hr.UpdatedInput != nil {
			result.UpdatedInput = hr.UpdatedInput
		}
		if hr.AdditionalContext != "" {
			if result.AdditionalContext != "" {
				result.AdditionalContext += "\n" + hr.AdditionalContext
			} else {
				result.AdditionalContext = hr.AdditionalContext
			}
		}
	}
	return result
}

// hookTimeoutForEvent returns the default timeout for a given event category.

// runOne routes hook execution based on its Type
func (hm *HookManager) runOne(event HookEvent, def HookDef, ctx HookContext) HookResult {
	switch def.Type {
	case HookTypeHTTP:
		return hm.runHTTP(event, def, ctx)
	case HookTypePrompt:
		return hm.runLLMPrompt(event, def, ctx)
	default:
		return hm.runCommand(event, def, ctx)
	}
}

// runHTTP executes an HTTP POST hook, passing and returning structured JSON
func (hm *HookManager) runHTTP(event HookEvent, def HookDef, ctx HookContext) HookResult {
	start := time.Now()
	timeout := hookTimeoutForEvent(event)
	if def.Timeout > 0 {
		timeout = time.Duration(def.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 1. Expand environment variables in URL
	url := os.ExpandEnv(def.URL)

	// 2. Build JSON payload
	payloadMap := map[string]any{
		"hookEventName": string(event),
		"tool_name":     ctx.ToolName,
		"tool_input":    ctx.ToolInput,
		"session_id":    ctx.SessionID,
	}
	if ctx.Prompt != "" {
		payloadMap["prompt"] = ctx.Prompt
	}
	if ctx.ToolOutput != "" {
		payloadMap["tool_output"] = ctx.ToolOutput
	}
	if ctx.ToolError != "" {
		payloadMap["tool_error"] = ctx.ToolError
	}

	jsonBytes, err := json.Marshal(payloadMap)
	if err != nil {
		return HookResult{Blocked: true, BlockReason: fmt.Sprintf("failed to marshal http hook payload: %v", err)}
	}

	// 3. Create HTTP Request
	req, err := http.NewRequestWithContext(execCtx, "POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return HookResult{Blocked: true, BlockReason: fmt.Sprintf("failed to create http request: %v", err)}
	}
	req.Header.Set("Content-Type", "application/json")

	// Apply custom headers and expand env variables (restricted by AllowedEnvVars if defined)
	for k, v := range def.Headers {
		expandedVal := v
		if len(def.AllowedEnvVars) > 0 {
			// Only expand allowed env variables
			for _, allowed := range def.AllowedEnvVars {
				placeholder := fmt.Sprintf("$%s", allowed)
				placeholderBraces := fmt.Sprintf("${%s}", allowed)
				envVal := os.Getenv(allowed)
				expandedVal = strings.ReplaceAll(expandedVal, placeholder, envVal)
				expandedVal = strings.ReplaceAll(expandedVal, placeholderBraces, envVal)
			}
		} else {
			expandedVal = os.ExpandEnv(v)
		}
		req.Header.Set(k, expandedVal)
	}

	// 4. Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	durationMS := time.Since(start).Milliseconds()

	if err != nil {
		if execCtx.Err() != nil {
			if def.OnTimeout == "block" {
				return HookResult{Blocked: true, BlockReason: fmt.Sprintf("http hook timed out after %v", timeout)}
			}
			return HookResult{}
		}
		return HookResult{Blocked: true, BlockReason: fmt.Sprintf("http hook execution failed: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return HookResult{Blocked: true, BlockReason: fmt.Sprintf("http hook returned status code: %d", resp.StatusCode)}
	}

	// 5. Parse response JSON
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return HookResult{Blocked: true, BlockReason: fmt.Sprintf("failed to read http hook response body: %v", err)}
	}

	return parseJSONResult(event, bodyBytes, durationMS, ctx, 0)
}

// runLLMPrompt executes an LLM-based prompt compliance hook
func (hm *HookManager) runLLMPrompt(event HookEvent, def HookDef, ctx HookContext) HookResult {
	start := time.Now()
	if globalLLMModel == nil {
		// Fallback: if model is not registered yet, proceed silently
		return HookResult{}
	}

	timeout := hookTimeoutForEvent(event)
	if def.Timeout > 0 {
		timeout = time.Duration(def.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 1. Interpolate context fields inside the LLM prompt
	interpolatedPrompt := def.Prompt
	interpolatedPrompt = strings.ReplaceAll(interpolatedPrompt, "$TOOL_NAME", ctx.ToolName)
	if ctx.ToolInput != nil {
		inputJSON, _ := json.Marshal(ctx.ToolInput)
		interpolatedPrompt = strings.ReplaceAll(interpolatedPrompt, "$TOOL_INPUT", string(inputJSON))
	}
	interpolatedPrompt = strings.ReplaceAll(interpolatedPrompt, "$PROMPT", ctx.Prompt)
	interpolatedPrompt = strings.ReplaceAll(interpolatedPrompt, "$TOOL_OUTPUT", ctx.ToolOutput)
	interpolatedPrompt = strings.ReplaceAll(interpolatedPrompt, "$TOOL_ERROR", ctx.ToolError)
	interpolatedPrompt = strings.ReplaceAll(interpolatedPrompt, "$SESSION_ID", ctx.SessionID)

	// 2. Build system instructions
	systemPrompt := `You are a strict security and policy compliance auditor. Your job is to audit tool requests. 
Evaluate the request and output a valid JSON response in EXACTLY this format (do not output any markdown code blocks, paragraphs, or extra text):
{
  "decision": "allow",
  "reason": ""
}
or:
{
  "decision": "deny",
  "reason": "<explain the specific security concern>"
}
`

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					{Text: systemPrompt + "\n\n[PROMPT TO AUDIT]:\n" + interpolatedPrompt},
				},
			},
		},
	}

	// 3. Invoke active LLM model
	var responseBuilder strings.Builder
	events := globalLLMModel.GenerateContent(execCtx, req, false)
	for resp, err := range events {
		if err != nil {
			return HookResult{Blocked: true, BlockReason: fmt.Sprintf("llm-prompt hook generation failed: %v", err)}
		}
		if resp != nil && resp.Content != nil {
			for _, part := range resp.Content.Parts {
				if part.Text != "" {
					responseBuilder.WriteString(part.Text)
				}
			}
		}
	}

	durationMS := time.Since(start).Milliseconds()

	// Extract JSON block from response text (supporting optional markdown ```json code blocks)
	responseText := strings.TrimSpace(responseBuilder.String())
	if strings.Contains(responseText, "```") {
		parts := strings.Split(responseText, "```")
		for _, part := range parts {
			trimmedPart := strings.TrimSpace(part)
			if strings.HasPrefix(trimmedPart, "json") {
				responseText = strings.TrimSpace(strings.TrimPrefix(trimmedPart, "json"))
				break
			}
			if strings.HasPrefix(trimmedPart, "{") && strings.HasSuffix(trimmedPart, "}") {
				responseText = trimmedPart
				break
			}
		}
	}

	return parseJSONResult(event, []byte(responseText), durationMS, ctx, 0)
}

// runCommand executes a shell subprocess hook (legacy mode)
func (hm *HookManager) runCommand(event HookEvent, def HookDef, ctx HookContext) HookResult {
	start := time.Now()

	// Build environment variables
	extraEnv := []string{
		"HOOK_EVENT=" + string(event),
	}
	if ctx.ToolName != "" {
		extraEnv = append(extraEnv, "HOOK_TOOL_NAME="+ctx.ToolName)
	}
	if ctx.ToolInput != nil {
		inputJSON, _ := json.Marshal(ctx.ToolInput)
		extraEnv = append(extraEnv, "HOOK_TOOL_INPUT="+hookTruncate(string(inputJSON), 10000))
	}
	if ctx.ToolOutput != "" {
		extraEnv = append(extraEnv, "HOOK_TOOL_OUTPUT="+hookTruncate(ctx.ToolOutput, 10000))
	}
	if ctx.ToolError != "" {
		extraEnv = append(extraEnv, "HOOK_TOOL_ERROR="+ctx.ToolError)
	}
	if ctx.Prompt != "" {
		extraEnv = append(extraEnv, "HOOK_PROMPT="+hookTruncate(ctx.Prompt, 10000))
	}
	if ctx.ResponseLength > 0 {
		extraEnv = append(extraEnv, "HOOK_RESPONSE_LENGTH="+strconv.Itoa(ctx.ResponseLength))
	}
	if ctx.CompactionType != "" {
		extraEnv = append(extraEnv, "HOOK_COMPACTION_TYPE="+ctx.CompactionType)
	}
	if ctx.SessionID != "" {
		extraEnv = append(extraEnv, "HOOK_SESSION_ID="+ctx.SessionID)
	}

	timeout := hookTimeoutForEvent(event)
	if def.Timeout > 0 {
		timeout = time.Duration(def.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", def.Command)
	cmd.Env = append(os.Environ(), extraEnv...)

	// Populate Stdin JSON payload
	var stdinBuf bytes.Buffer
	stdinMap := map[string]any{
		"tool_name":  ctx.ToolName,
		"tool_input": ctx.ToolInput,
		"session_id": ctx.SessionID,
	}
	if ctx.Prompt != "" {
		stdinMap["prompt"] = ctx.Prompt
	}
	if ctx.ToolOutput != "" {
		stdinMap["tool_output"] = ctx.ToolOutput
	}
	if ctx.ToolError != "" {
		stdinMap["tool_error"] = ctx.ToolError
	}
	_ = json.NewEncoder(&stdinBuf).Encode(stdinMap)
	cmd.Stdin = &stdinBuf

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	durationMS := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		if execCtx.Err() != nil {
			fmt.Fprintf(os.Stderr, "[hook:%s] timeout (%v)\n", event, timeout)
			LogError(CatSession, "hook_timeout", fmt.Sprintf("Hook command timed out after %v", timeout), execCtx.Err(), map[string]any{
				"event":       event,
				"command":     def.Command,
				"tool":        ctx.ToolName,
				"duration_ms": durationMS,
			})
			if def.OnTimeout == "block" {
				return HookResult{Blocked: true, BlockReason: fmt.Sprintf("hook timed out after %v", timeout)}
			}
			return HookResult{}
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			LogError(CatSession, "hook_crash", "Hook command crashed unexpectedly", err, map[string]any{
				"event":       event,
				"command":     def.Command,
				"tool":        ctx.ToolName,
				"duration_ms": durationMS,
			})
			return HookResult{Blocked: true, BlockReason: fmt.Sprintf("hook crashed: %v", err)}
		}
	}

	stdoutBytes := stdout.Bytes()
	isJSON := false
	var stdoutJSON struct {
		Decision           string         `json:"decision,omitempty"`
		Reason             string         `json:"reason,omitempty"`
		Message            string         `json:"message,omitempty"`
		Modifications      map[string]any `json:"modifications,omitempty"`
		HookSpecificOutput *struct {
			HookEventName            string         `json:"hookEventName,omitempty"`
			PermissionDecision       string         `json:"permissionDecision,omitempty"`
			PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
			UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
			AdditionalContext        string         `json:"additionalContext,omitempty"`
		} `json:"hookSpecificOutput,omitempty"`
	}

	if len(stdoutBytes) > 0 {
		if unmarshalErr := json.Unmarshal(stdoutBytes, &stdoutJSON); unmarshalErr == nil {
			isJSON = true
		}
	}

	if isJSON {
		return parseJSONResult(event, stdoutBytes, durationMS, ctx, exitCode)
	}

	// Legacy exit-code protocol
	blocked := false
	blockReason := ""
	var messages []string

	switch exitCode {
	case 0:
		// silent success
	case 1:
		blocked = true
		blockReason = strings.TrimSpace(stderr.String())
		if blockReason == "" {
			blockReason = "blocked by hook exit code 1"
		}
	case 2:
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			messages = []string{msg}
		}
	default:
		blocked = true
		blockReason = fmt.Sprintf("hook exited with unexpected code %d: %s", exitCode, strings.TrimSpace(stderr.String()))
	}

	if blocked {
		LogAudit(CatSecurity, "hook_execute_blocked", fmt.Sprintf("Hook blocked operation: %s", blockReason), map[string]any{
			"event":       event,
			"command":     def.Command,
			"tool":        ctx.ToolName,
			"reason":      blockReason,
			"duration_ms": durationMS,
		})
		return HookResult{Blocked: true, BlockReason: blockReason}
	}

	return HookResult{
		Messages: messages,
	}
}

// helper to parse Hook response JSON
