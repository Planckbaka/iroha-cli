package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
	"sync/atomic"
)

var anthropicToolIDCounter uint64

func nextAnthropicToolID() string {
	n := atomic.AddUint64(&anthropicToolIDCounter, 1)
	return fmt.Sprintf("toolu_%04d", n)
}

// AnthropicAdapter implements model.LLM for the Anthropic Messages API.
type AnthropicAdapter struct {
	modelName        string
	apiKey           string
	baseURL          string
	systemPrompt     string
	hooks            AdapterHooks
	cumulativeTokens int
}

func NewAnthropicAdapter(modelName, apiKey, baseURL, systemPrompt string, hooks AdapterHooks) *AnthropicAdapter {
	if modelName == "" {
		modelName = "claude-sonnet-4-6"
	}
	return &AnthropicAdapter{
		modelName:    modelName,
		apiKey:       apiKey,
		baseURL:      baseURL,
		systemPrompt: systemPrompt,
		hooks:        hooks,
	}
}

func (a *AnthropicAdapter) Name() string {
	return a.modelName
}

func (a *AnthropicAdapter) CumulativeTokens() int {
	return a.cumulativeTokens
}

func (a *AnthropicAdapter) AddTokens(n int) {
	a.cumulativeTokens += n
}

// Anthropic Messages API types

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// For tool_result blocks
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content_  json.RawMessage `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicSystemBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicRequest struct {
	Model     string                 `json:"model"`
	MaxTokens int                    `json:"max_tokens"`
	Messages  []anthropicMessage     `json:"messages"`
	System    []anthropicSystemBlock `json:"system,omitempty"`
	Tools     []anthropicTool        `json:"tools,omitempty"`
	Stream    bool                   `json:"stream"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicTextDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicToolUseDelta struct {
	Type  string `json:"type"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input string `json:"partial_json,omitempty"`
}

type anthropicMessageDelta struct {
	Type  string              `json:"type"`
	Delta anthropicUsageDelta `json:"delta"`
	Usage anthropicUsage      `json:"usage"`
}

type anthropicUsageDelta struct {
	StopReason string `json:"stop_reason"`
}

func (a *AnthropicAdapter) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if a.apiKey == "" {
		return func(yield func(*model.LLMResponse, error) bool) {
			yield(nil, fmt.Errorf("Anthropic adapter requires an API key"))
		}
	}

	return func(yield func(*model.LLMResponse, error) bool) {
		if a.hooks != nil {
			a.hooks.NoteRound()
		}

		// Build system prompt
		var systemPrompt string
		if a.systemPrompt != "" {
			systemPrompt = a.systemPrompt
		} else if req.Config != nil && req.Config.SystemInstruction != nil {
			var parts []string
			for _, p := range req.Config.SystemInstruction.Parts {
				if p.Text != "" {
					parts = append(parts, p.Text)
				}
			}
			systemPrompt = strings.Join(parts, "\n")
		}

		// Inject Nag Reminder if triggered
		if a.hooks != nil {
			nagMsg := a.hooks.NagReminder()
			if nagMsg != "" {
				for i := len(req.Contents) - 1; i >= 0; i-- {
					c := req.Contents[i]
					if c.Role == "user" {
						if len(c.Parts) > 0 && c.Parts[0].Text != "" {
							c.Parts[0].Text = nagMsg + "\n\n" + c.Parts[0].Text
							break
						}
					}
				}
			}
		}
		compactedContents := req.Contents
		if a.hooks != nil {
			compactedContents = a.hooks.CompactHistory(req.Contents)
		}

		// Convert genai Contents to Anthropic messages
		messages, err := convertToAnthropicMessages(compactedContents)
		if err != nil {
			yield(nil, err)
			return
		}

		// Convert tools
		var tools []anthropicTool
		if req.Config != nil && req.Config.Tools != nil {
			for _, t := range req.Config.Tools {
				if t != nil && t.FunctionDeclarations != nil {
					for _, fd := range t.FunctionDeclarations {
						if fd != nil {
							var schema json.RawMessage
							if fd.ParametersJsonSchema != nil {
								schema, _ = json.Marshal(fd.ParametersJsonSchema)
							} else if fd.Parameters != nil {
								schema, _ = json.Marshal(fd.Parameters)
							}
							if schema == nil {
								schema = json.RawMessage(`{"type":"object","properties":{}}`)
							}
							tools = append(tools, anthropicTool{
								Name:        fd.Name,
								Description: fd.Description,
								InputSchema: schema,
							})
						}
					}
				}
			}
		}

		toolNames := make([]string, 0, len(tools))
		for _, t := range tools {
			toolNames = append(toolNames, t.Name)
		}
		DebugLog("[Anthropic] Sending %d tools: %v | Model: %s", len(tools), toolNames, a.modelName)
		// Build system blocks with prompt caching on the last block
		var systemBlocks []anthropicSystemBlock
		if systemPrompt != "" {
			systemBlocks = append(systemBlocks, anthropicSystemBlock{
				Type:         "text",
				Text:         systemPrompt,
				CacheControl: &anthropicCacheControl{Type: "ephemeral"},
			})
		}
		// Build request
		anthropicReq := anthropicRequest{
			Model:     a.modelName,
			MaxTokens: 8192,
			Messages:  messages,
			System:    systemBlocks,
			Tools:     tools,
			Stream:    true,
		}

		reqBytes, err := json.Marshal(anthropicReq)
		if err != nil {
			yield(nil, fmt.Errorf("marshal anthropic request: %w", err))
			return
		}

		// Determine API URL
		apiURL := a.baseURL
		if apiURL == "" {
			apiURL = "https://api.anthropic.com"
		}
		if !strings.HasSuffix(apiURL, "/v1/messages") {
			apiURL = strings.TrimSuffix(apiURL, "/") + "/v1/messages"
		}

		// Send HTTP request with retry
		var resp *http.Response
		var lastErr error
		maxRetries := 3

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				// Check session retry budget before retrying.
				if !ConsumeRetry() {
					yield(nil, budgetExhaustedError(a.modelName, lastErr))
					return
				}

				delay := time.Duration(1<<uint(attempt-1)) * time.Second

				// Override with Retry-After header value if available.
				if resp != nil {
					if raSec := parseRetryAfter(resp); raSec > 0 {
						delay = time.Duration(raSec * float64(time.Second))
					}
				}

				select {
				case <-ctx.Done():
					yield(nil, ctx.Err())
					return
				case <-time.After(delay):
				}
			}

			httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBytes))
			if err != nil {
				lastErr = fmt.Errorf("create HTTP request: %w", err)
				continue
			}

			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("x-api-key", a.apiKey)
			httpReq.Header.Set("anthropic-version", "2023-06-01")

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err = client.Do(httpReq)
			if err != nil {
				lastErr = fmt.Errorf("anthropic API call failed: %w", err)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				isTransient := resp.StatusCode == 429 || resp.StatusCode >= 500
				lastErr = fmt.Errorf("anthropic API error %d: %s", resp.StatusCode, string(bodyBytes))
				if isTransient {
					continue
				}
				yield(nil, lastErr)
				return
			}

			lastErr = nil
			break
		}

		if lastErr != nil {
			yield(nil, lastErr)
			return
		}

		defer func() { _ = resp.Body.Close() }()

		// Parse SSE stream with state machine
		reader := bufio.NewReader(resp.Body)
		var (
			currentToolName string
			toolInputParts  []string
			sentFinal       bool
		)

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				yield(nil, fmt.Errorf("read anthropic SSE: %w", err))
				return
			}

			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "event: ") {
				continue
			}

			eventType := strings.TrimPrefix(line, "event: ")

			// Read the data line that follows
			dataLine, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				continue
			}
			dataLine = strings.TrimSpace(dataLine)
			if !strings.HasPrefix(dataLine, "data: ") {
				continue
			}
			dataStr := strings.TrimPrefix(dataLine, "data: ")

			switch eventType {
			case "message_start":
				// Initialize — parse usage if present
				var msgStart struct {
					Message struct {
						Usage anthropicUsage `json:"usage"`
					} `json:"message"`
				}
				if err := json.Unmarshal([]byte(dataStr), &msgStart); err == nil {
					a.AddTokens(msgStart.Message.Usage.InputTokens)
				}

			case "content_block_start":
				var blockStart struct {
					Index        int                    `json:"index"`
					ContentBlock *anthropicContentBlock `json:"content_block"`
				}
				if err := json.Unmarshal([]byte(dataStr), &blockStart); err == nil && blockStart.ContentBlock != nil {
					if blockStart.ContentBlock.Type == "tool_use" {
						currentToolName = blockStart.ContentBlock.Name
						toolInputParts = nil
					}
				}

			case "content_block_delta":
				var delta struct {
					Delta json.RawMessage `json:"delta"`
				}
				if err := json.Unmarshal([]byte(dataStr), &delta); err != nil {
					continue
				}

				// Try text delta
				var textDelta anthropicTextDelta
				if err := json.Unmarshal(delta.Delta, &textDelta); err == nil && textDelta.Type == "text_delta" {
					if textDelta.Text != "" {
						if !yield(&model.LLMResponse{
							Content: &genai.Content{
								Role: "model",
								Parts: []*genai.Part{
									{Text: textDelta.Text},
								},
							},
							Partial:      true,
							TurnComplete: false,
						}, nil) {
							return
						}
					}
					continue
				}

				// Try input_json_delta
				var inputDelta anthropicToolUseDelta
				if err := json.Unmarshal(delta.Delta, &inputDelta); err == nil && inputDelta.Type == "input_json_delta" {
					toolInputParts = append(toolInputParts, inputDelta.Input)
				}

			case "content_block_stop":
				// If we were accumulating a tool call, yield it
				if currentToolName != "" {
					var parsedArgs map[string]any
					fullInput := strings.Join(toolInputParts, "")
					_ = json.Unmarshal([]byte(fullInput), &parsedArgs)
					DebugLog("[Anthropic TOOL-CALL] Yielding FunctionCall: name=%s args=%v", currentToolName, parsedArgs)

					if !yield(&model.LLMResponse{
						Content: &genai.Content{
							Role: "model",
							Parts: []*genai.Part{
								{
									FunctionCall: &genai.FunctionCall{
										Name: currentToolName,
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

					currentToolName = ""
					toolInputParts = nil
				}

			case "message_delta":
				var msgDelta anthropicMessageDelta
				if err := json.Unmarshal([]byte(dataStr), &msgDelta); err == nil {
					a.AddTokens(msgDelta.Usage.OutputTokens)
				}

			case "message_stop":
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

			case "error":
				var errResp struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(dataStr), &errResp); err == nil {
					yield(nil, fmt.Errorf("anthropic API error: %s", errResp.Error.Message))
				} else {
					yield(nil, fmt.Errorf("anthropic API error: %s", dataStr))
				}
				return

			case "ping":
				// Ignore
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

// convertToAnthropicMessages converts genai.Content slices to Anthropic message format.
func convertToAnthropicMessages(contents []*genai.Content) ([]anthropicMessage, error) {
	var messages []anthropicMessage
	toolIDMap := make(map[string]string)

	for _, c := range contents {
		role := c.Role
		if role == "" || role == "model" {
			role = "assistant"
		}

		var blocks []anthropicContentBlock

		for _, p := range c.Parts {
			if p.Text != "" {
				blocks = append(blocks, anthropicContentBlock{
					Type: "text",
					Text: p.Text,
				})
			}

			if p.FunctionCall != nil {
				argsJSON, _ := json.Marshal(p.FunctionCall.Args)
				toolID := nextAnthropicToolID()
				toolIDMap[p.FunctionCall.Name] = toolID
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    toolID,
					Name:  p.FunctionCall.Name,
					Input: argsJSON,
				})
			}

			if p.FunctionResponse != nil {
				role = "user"
				respJSON, _ := json.Marshal(p.FunctionResponse.Response)
				mappedID := toolIDMap[p.FunctionResponse.Name]
				if mappedID == "" {
					mappedID = "toolu_" + p.FunctionResponse.Name
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:      "tool_result",
					ToolUseID: mappedID,
					Content_:  respJSON,
				})
			}
		}

		if len(blocks) > 0 {
			messages = append(messages, anthropicMessage{
				Role:    role,
				Content: blocks,
			})
		}
	}

	return messages, nil
}
