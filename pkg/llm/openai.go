package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// OpenAICompatibleAdapter integrates Zhipu AI GLM-4 (OpenAI-compatible) into ADK
type OpenAICompatibleAdapter struct {
	modelName        string
	apiKey           string
	baseURL          string
	systemPrompt     string
	hooks            AdapterHooks
	cumulativeTokens int
}

func NewOpenAICompatibleAdapter(modelName string, apiKey string, baseURL string, systemPrompt string, hooks AdapterHooks) *OpenAICompatibleAdapter {
	if modelName == "" {
		modelName = "glm-4"
	}
	return &OpenAICompatibleAdapter{
		modelName:    modelName,
		apiKey:       apiKey,
		baseURL:      baseURL,
		systemPrompt: systemPrompt,
		hooks:        hooks,
	}
}

func (g *OpenAICompatibleAdapter) Name() string {
	return g.modelName
}

func (g *OpenAICompatibleAdapter) CumulativeTokens() int {
	return g.cumulativeTokens
}

func (g *OpenAICompatibleAdapter) AddTokens(n int) {
	g.cumulativeTokens += n
}

// Zhipu GLM-4 API structures (OpenAI compatible)
type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	Tools      []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatToolSchema struct {
	Type     string             `json:"type"`
	Function chatFunctionSchema `json:"function"`
}

type chatFunctionSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

type chatRequest struct {
	Model    string           `json:"model"`
	Messages []chatMessage    `json:"messages"`
	Tools    []chatToolSchema `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
}

type chatStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func (g *OpenAICompatibleAdapter) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if g.apiKey == "" {
		return func(yield func(*model.LLMResponse, error) bool) {
			yield(nil, fmt.Errorf("OpenAI-compatible adapter requires an API key"))
		}
	}

	return func(yield func(*model.LLMResponse, error) bool) {
		if g.hooks != nil {
			g.hooks.NoteRound()
		}

		compactedContents := req.Contents
		if g.hooks != nil {
			compactedContents = g.hooks.CompactHistory(req.Contents)
		}

		if g.hooks != nil {
			nagMsg := g.hooks.NagReminder()
			if nagMsg != "" {
				for i := len(compactedContents) - 1; i >= 0; i-- {
					c := compactedContents[i]
					if c.Role == "user" {
						if len(c.Parts) > 0 && c.Parts[0].Text != "" {
							c.Parts[0].Text = nagMsg + "\n\n" + c.Parts[0].Text
							break
						}
					}
				}
			}
		}

		var systemPrompt string
		if g.systemPrompt != "" {
			systemPrompt = g.systemPrompt
		} else if req.Config != nil && req.Config.SystemInstruction != nil {
			var parts []string
			for _, p := range req.Config.SystemInstruction.Parts {
				if p.Text != "" {
					parts = append(parts, p.Text)
				}
			}
			systemPrompt = strings.Join(parts, "\n")
		}

		// 1. Convert compactedContents to message list
		var messages []chatMessage
		if systemPrompt != "" {
			messages = append(messages, chatMessage{
				Role:    "system",
				Content: systemPrompt,
			})
		}
		for _, c := range compactedContents {
			role := c.Role
			if role == "" || role == "model" {
				role = "assistant"
			}

			var textParts []string
			var toolCalls []chatToolCall

			for _, part := range c.Parts {
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
				if part.FunctionCall != nil {
					argsBytes, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, chatToolCall{
						ID:   "call_" + part.FunctionCall.Name,
						Type: "function",
						Function: chatFunction{
							Name:      part.FunctionCall.Name,
							Arguments: string(argsBytes),
						},
					})
				}
			}

			if len(textParts) > 0 || len(toolCalls) > 0 || (role != "tool") {
				var content any = strings.Join(textParts, "\n")
				messages = append(messages, chatMessage{
					Role:    role,
					Content: content,
					Tools:   toolCalls,
				})
			}

			// US-003: Each FunctionResponse emits a separate message
			for _, part := range c.Parts {
				if part.FunctionResponse != nil {
					respBytes, _ := json.Marshal(part.FunctionResponse.Response)
					messages = append(messages, chatMessage{
						Role:       "tool",
						Content:    string(respBytes),
						ToolCallID: "call_" + part.FunctionResponse.Name,
					})
				}
			}
		}

		// 2. Map ADK Tools to schema
		var tools []chatToolSchema
		if req.Config != nil && req.Config.Tools != nil && len(req.Config.Tools) > 0 {
			for _, t := range req.Config.Tools {
				if t != nil && t.FunctionDeclarations != nil {
					for _, fd := range t.FunctionDeclarations {
						if fd != nil {
							var params any = fd.ParametersJsonSchema
							if params == nil {
								params = fd.Parameters
							}
							tools = append(tools, chatToolSchema{
								Type: "function",
								Function: chatFunctionSchema{
									Name:        fd.Name,
									Description: fd.Description,
									Parameters:  params,
								},
							})
						}
					}
				}
			}
		}

		// 3. Construct request
		glmReq := chatRequest{
			Model:    g.modelName,
			Messages: messages,
			Tools:    tools,
			Stream:   true,
		}

		toolNames := make([]string, 0, len(tools))
		for _, t := range tools {
			toolNames = append(toolNames, t.Function.Name)
		}
		DebugLog("Sending %d tools: %v | Model: %s", len(tools), toolNames, g.modelName)

		reqBytes, err := json.Marshal(glmReq)
		if err != nil {
			if !yield(nil, fmt.Errorf("failed to serialize GLM request: %w", err)) {
				return
			}
			return
		}

		// 4. Send HTTP request with retry
		apiURL := g.baseURL
		if apiURL == "" {
			if !yield(nil, fmt.Errorf("OpenAI-compatible adapter requires a base URL; set --base-url or provider config")) {
				return
			}
			return
		}
		if !strings.HasSuffix(apiURL, "/chat/completions") {
			apiURL = strings.TrimSuffix(apiURL, "/") + "/chat/completions"
		}

		var resp *http.Response
		var lastErr error
		maxRetries := 3

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				// Check session retry budget before retrying.
				if !ConsumeRetry() {
					if !yield(nil, budgetExhaustedError(g.modelName, lastErr)) {
						return
					}
					return
				}

				delaySec := 1.0 * math.Pow(2.0, float64(attempt-1))
				jitter := (rand.Float64() * 0.4) - 0.2
				delaySec = delaySec + (delaySec * jitter)

				// Override with Retry-After header value if available.
				// (resp may carry the header from the previous 429 attempt.)
				if resp != nil {
					if ra := parseRetryAfter(resp); ra > 0 {
						delaySec = ra
					}
				}

				if delaySec > 60.0 {
					delaySec = 60.0
				}
				if delaySec < 1.0 {
					delaySec = 1.0
				}

				warnMsg := fmt.Sprintf("\n⚠️  [Network Error] Retrying attempt %d/%d, waiting ~%.1f seconds...\n", attempt, maxRetries, delaySec)
				if !yield(&model.LLMResponse{
					Content: &genai.Content{
						Role: "model",
						Parts: []*genai.Part{
							{Text: warnMsg},
						},
					},
					Partial:      true,
					TurnComplete: false,
				}, nil) {
					return
				}

				select {
				case <-ctx.Done():
					if !yield(nil, ctx.Err()) {
						return
					}
					return
				case <-time.After(time.Duration(delaySec * float64(time.Second))):
				}
			}

			httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBytes))
			if err != nil {
				lastErr = fmt.Errorf("failed to create HTTP request: %w", err)
				continue
			}

			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

			client := &http.Client{
				Timeout: 30 * time.Second,
			}

			resp, err = client.Do(httpReq)
			if err != nil {
				lastErr = fmt.Errorf("LLM API call (%s) failed: %w", g.modelName, err)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()

				isTransient := resp.StatusCode == 429 || resp.StatusCode >= 500
				lastErr = fmt.Errorf("LLM API (%s) returned error code %d: %s", g.modelName, resp.StatusCode, string(bodyBytes))

				if isTransient {
					continue
				} else {
					if !yield(nil, lastErr) {
						return
					}
					return
				}
			}

			lastErr = nil
			break
		}

		if lastErr != nil {
			if !yield(nil, lastErr) {
				return
			}
			return
		}

		defer func() { _ = resp.Body.Close() }()

		// 5. Parse Server-Sent Events (SSE) stream
		// US-008: Track multiple tool calls by index
		type toolAccumulator struct {
			name string
			args strings.Builder
		}
		pendingTools := make(map[int]*toolAccumulator)
		var sentFinal bool

		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				if !yield(nil, fmt.Errorf("error reading API response stream: %w", err)) {
					return
				}
				return
			}

			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "data: ") {
				continue
			}

			dataStr := strings.TrimPrefix(line, "data: ")
			if dataStr == "[DONE]" {
				break
			}

			var chunk chatStreamResponse
			if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
				continue
			}

			if chunk.Usage.TotalTokens > 0 {
				g.AddTokens(chunk.Usage.TotalTokens)
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			delta := choice.Delta

			if choice.FinishReason != "" || len(delta.ToolCalls) > 0 {
				DebugLog("[SSE] finish=%s toolCalls=%d contentLen=%d pendingTools=%d", choice.FinishReason, len(delta.ToolCalls), len(delta.Content), len(pendingTools))
			}

			// 1. Accumulate tool call deltas by index
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				acc, ok := pendingTools[idx]
				if !ok {
					acc = &toolAccumulator{}
					pendingTools[idx] = acc
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.args.WriteString(tc.Function.Arguments)
				}
			}

			// 2. On finish_reason, yield all accumulated tool calls with TurnComplete: false
			if choice.FinishReason != "" && len(pendingTools) > 0 {
				for i := 0; i < len(pendingTools); i++ {
					acc, ok := pendingTools[i]
					if !ok || acc.name == "" {
						continue
					}
					var parsedArgs map[string]any
					_ = json.Unmarshal([]byte(acc.args.String()), &parsedArgs)
					DebugLog("[TOOL-CALL] Yielding FunctionCall: name=%s args=%v finish=%s", acc.name, parsedArgs, choice.FinishReason)

					if !yield(&model.LLMResponse{
						Content: &genai.Content{
							Role: "model",
							Parts: []*genai.Part{
								{
									FunctionCall: &genai.FunctionCall{
										Name: acc.name,
										Args: parsedArgs,
									},
								},
							},
						},
						Partial:      false,
						TurnComplete: false,
					}, nil) {
						return
					}
				}
				pendingTools = make(map[int]*toolAccumulator)
			}

			// 3. Skip text processing for tool-only chunks
			if len(delta.ToolCalls) > 0 {
				continue
			}

			// 4. Stream text content
			if delta.Content != "" {
				if !yield(&model.LLMResponse{
					Content: &genai.Content{
						Role: "model",
						Parts: []*genai.Part{
							{Text: delta.Content},
						},
					},
					Partial:      true,
					TurnComplete: false,
				}, nil) {
					return
				}
			}

			// 5. Finish reason with no pending tools → TurnComplete: true
			if choice.FinishReason != "" {
				if !yield(&model.LLMResponse{
					Content: &genai.Content{
						Role: "model",
						Parts: []*genai.Part{
							{Text: ""},
						},
					},
					Partial:      false,
					TurnComplete: true,
				}, nil) {
					return
				}
				sentFinal = true
				break
			}
		}

		// Flush any remaining tool calls if stream ended prematurely
		for i := 0; i < len(pendingTools); i++ {
			acc, ok := pendingTools[i]
			if !ok || acc.name == "" {
				continue
			}
			var parsedArgs map[string]any
			_ = json.Unmarshal([]byte(acc.args.String()), &parsedArgs)

			if !yield(&model.LLMResponse{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{
							FunctionCall: &genai.FunctionCall{
								Name: acc.name,
								Args: parsedArgs,
							},
						},
					},
				},
				Partial:      false,
				TurnComplete: false,
			}, nil) {
				return
			}
		}

		// Guarantee a final response
		if !sentFinal {
			if !yield(&model.LLMResponse{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{Text: ""},
					},
				},
				Partial:      false,
				TurnComplete: true,
			}, nil) {
				return
			}
		}
	}
}
