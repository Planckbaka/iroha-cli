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
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// Decoupled callbacks to prevent circular dependencies
var NagReminderTrigger func() string
var NoteRoundWithoutUpdate func()

// GLMAdapter integrates Zhipu AI GLM-4 (OpenAI-compatible) into ADK
type GLMAdapter struct {
	modelName string
	apiKey    string
	baseURL   string
}

func NewGLMAdapter(modelName string, apiKey string, baseURL string) *GLMAdapter {
	if modelName == "" {
		modelName = "glm-4"
	}
	return &GLMAdapter{
		modelName: modelName,
		apiKey:    apiKey,
		baseURL:   baseURL,
	}
}

func (g *GLMAdapter) Name() string {
	return g.modelName
}

// Zhipu GLM-4 API structures (OpenAI compatible)
type glmMessage struct {
	Role    string        `json:"role"`
	Content any           `json:"content,omitempty"`
	Tools   []glmToolCall `json:"tool_calls,omitempty"`
}

type glmToolCall struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function glmFunction `json:"function"`
}

type glmFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type glmToolSchema struct {
	Type     string            `json:"type"`
	Function glmFunctionSchema `json:"function"`
}

type glmFunctionSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

type glmChatRequest struct {
	Model    string          `json:"model"`
	Messages []glmMessage    `json:"messages"`
	Tools    []glmToolSchema `json:"tools,omitempty"`
	Stream   bool            `json:"stream"`
}

type glmStreamResponse struct {
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
}

// CompactRequestContents checks for large tool outputs and compacts req.Contents.
// It also compresses older conversation rounds if total rounds > 12.
func (g *GLMAdapter) CompactRequestContents(req *model.LLMRequest) []*genai.Content {
	if len(req.Contents) == 0 {
		return nil
	}

	// 1. Deep copy req.Contents so we don't modify the session history held in memory.
	contents := make([]*genai.Content, len(req.Contents))
	for i, c := range req.Contents {
		contents[i] = &genai.Content{
			Role: c.Role,
		}
		if c.Parts != nil {
			contents[i].Parts = make([]*genai.Part, len(c.Parts))
			for j, p := range c.Parts {
				var fcCopy *genai.FunctionCall
				if p.FunctionCall != nil {
					fcCopy = &genai.FunctionCall{
						Name: p.FunctionCall.Name,
					}
					if p.FunctionCall.Args != nil {
						argsCopy := make(map[string]any)
						for k, v := range p.FunctionCall.Args {
							argsCopy[k] = v
						}
						fcCopy.Args = argsCopy
					}
				}

				var frCopy *genai.FunctionResponse
				if p.FunctionResponse != nil {
					frCopy = &genai.FunctionResponse{
						Name: p.FunctionResponse.Name,
					}
					if p.FunctionResponse.Response != nil {
						respCopy := make(map[string]any)
						for k, v := range p.FunctionResponse.Response {
							respCopy[k] = v
						}
						frCopy.Response = respCopy
					}
				}

				contents[i].Parts[j] = &genai.Part{
					Text:             p.Text,
					InlineData:       p.InlineData,
					FunctionCall:     fcCopy,
					FunctionResponse: frCopy,
				}
			}
		}
	}

	// 2. Perform Micro-Compaction of large tool outputs (FunctionResponse)
	sessionID := "session-default" // default session ID
	homeDir, _ := os.UserHomeDir()
	archiveDir := filepath.Join(homeDir, ".go-claude", "transcripts")
	archivePath := filepath.Join(archiveDir, sessionID+".jsonl")

	for _, c := range contents {
		for _, p := range c.Parts {
			if p != nil && p.FunctionResponse != nil && p.FunctionResponse.Response != nil {
				respBytes, _ := json.Marshal(p.FunctionResponse.Response)
				respStr := string(respBytes)

				if len(respStr) > 1000 {
					// Archive the full original output to JSONL
					_ = os.MkdirAll(archiveDir, 0755)
					f, err := os.OpenFile(archivePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					if err == nil {
						logEntry := map[string]any{
							"timestamp": time.Now().Format(time.RFC3339),
							"role":      "tool",
							"tool_name": p.FunctionResponse.Name,
							"content":   respStr,
						}
						entryBytes, _ := json.Marshal(logEntry)
						_, _ = f.Write(append(entryBytes, '\n'))
						_ = f.Close()
					}

					// Replace with micro-compaction placeholder
					p.FunctionResponse.Response = map[string]any{
						"output": fmt.Sprintf("[Tool \"%s\" output: %d bytes of output. (Full output archived to %s)]", 
							p.FunctionResponse.Name, len(respStr), archivePath),
					}
				}
			}
		}
	}

	// 3. Perform Conversational Summarization if total messages > 12
	if len(contents) > 12 {
		compacted := make([]*genai.Content, 0)
		
		// Keep the first round (which contains the user's initial prompt)
		compacted = append(compacted, contents[0])

		// Summarize the middle rounds (from index 1 to len(contents) - 5)
		var middlePrompts []string
		var middleTools []string
		
		for i := 1; i < len(contents)-4; i++ {
			c := contents[i]
			role := c.Role
			if role == "" || role == "model" {
				role = "assistant"
			}
			
			for _, p := range c.Parts {
				if p.Text != "" {
					if role == "user" {
						middlePrompts = append(middlePrompts, fmt.Sprintf("User: %s", p.Text))
					} else {
						middlePrompts = append(middlePrompts, fmt.Sprintf("Agent: %s", p.Text))
					}
				}
				if p.FunctionCall != nil {
					middleTools = append(middleTools, fmt.Sprintf("Called tool %s", p.FunctionCall.Name))
				}
				if p.FunctionResponse != nil {
					middleTools = append(middleTools, fmt.Sprintf("Tool %s responded", p.FunctionResponse.Name))
				}
			}
		}

		// Build a highly compact system instruction summarizing completed steps
		summaryContent := fmt.Sprintf("[System: Previous conversational history compacted. Summary of completed steps:\nPrompts: %s\nTools Executed: %s]", 
			strings.Join(middlePrompts, " | "), strings.Join(middleTools, ", "))

		systemSummary := &genai.Content{
			Role: "system",
			Parts: []*genai.Part{
				{Text: summaryContent},
			},
		}
		compacted = append(compacted, systemSummary)

		// Keep the last 4 rounds
		for i := len(contents)-4; i < len(contents); i++ {
			compacted = append(compacted, contents[i])
		}

		contents = compacted
	}

	return contents
}

func (g *GLMAdapter) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	// If API Key is empty or simulate, run simulated premium GLM-4 response
	if g.apiKey == "" || g.apiKey == "simulate" {
		return g.generateSimulatedGLM(ctx, req)
	}

	return func(yield func(*model.LLMResponse, error) bool) {
		// Note round without update to keep track of turns
		if NoteRoundWithoutUpdate != nil {
			NoteRoundWithoutUpdate()
		}

		// Perform Context Compaction (s06) on deep-copied req.Contents
		compactedContents := g.CompactRequestContents(req)

		// Inject Nag Reminder if triggered (s03)
		if NagReminderTrigger != nil {
			nagMsg := NagReminderTrigger()
			if nagMsg != "" {
				// Inject as prefix to the latest user message
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

		// 1. Convert compactedContents to Zhipu GLM message list
		var messages []glmMessage
		for _, c := range compactedContents {
			role := c.Role
			if role == "" || role == "model" {
				role = "assistant"
			}
			
			var textParts []string
			var toolCalls []glmToolCall

			for _, part := range c.Parts {
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
				if part.FunctionCall != nil {
					argsBytes, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, glmToolCall{
						ID:   "call_" + part.FunctionCall.Name,
						Type: "function",
						Function: glmFunction{
							Name:      part.FunctionCall.Name,
							Arguments: string(argsBytes),
						},
					})
				}
				// Handle FunctionResponse (Tool Output) back to GLM message
				if part.FunctionResponse != nil {
					role = "tool"
					respBytes, _ := json.Marshal(part.FunctionResponse.Response)
					textParts = append(textParts, string(respBytes))
				}
			}

			var content any
			if role == "tool" {
				content = strings.Join(textParts, "\n")
			} else {
				content = strings.Join(textParts, "\n")
			}

			messages = append(messages, glmMessage{
				Role:    role,
				Content: content,
				Tools:   toolCalls,
			})
		}

		// 2. Map ADK Tools to GLM Tools Schema
		var tools []glmToolSchema
		if req.Config != nil && req.Config.Tools != nil && len(req.Config.Tools) > 0 {
			for _, t := range req.Config.Tools {
				if t != nil && t.FunctionDeclarations != nil {
					for _, fd := range t.FunctionDeclarations {
						if fd != nil {
							var params any = fd.ParametersJsonSchema
							if params == nil {
								params = fd.Parameters
							}
							tools = append(tools, glmToolSchema{
								Type: "function",
								Function: glmFunctionSchema{
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

		// 3. Construct Zhipu GLM Request
		glmReq := glmChatRequest{
			Model:    g.modelName,
			Messages: messages,
			Tools:    tools,
			Stream:   true,
		}

		reqBytes, err := json.Marshal(glmReq)
		if err != nil {
			yield(nil, fmt.Errorf("序列化 GLM 请求失败: %w", err))
			return
		}

		// 4. Send HTTP request to dynamic endpoint
		apiURL := g.baseURL
		if apiURL == "" {
			apiURL = "https://open.bigmodel.cn/api/paas/v4/chat/completions"
		} else {
			if !strings.HasSuffix(apiURL, "/chat/completions") {
				apiURL = strings.TrimSuffix(apiURL, "/") + "/chat/completions"
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBytes))
		if err != nil {
			yield(nil, fmt.Errorf("创建 HTTP 请求失败: %w", err))
			return
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+g.apiKey)

		client := &http.Client{}
		resp, err := client.Do(httpReq)
		if err != nil {
			yield(nil, fmt.Errorf("调用智谱 API 失败: %w", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			yield(nil, fmt.Errorf("智谱 API 返回错误码 %d: %s", resp.StatusCode, string(bodyBytes)))
			return
		}

		// 5. Parse Server-Sent Events (SSE) stream
		reader := bufio.NewReader(resp.Body)
		var currentToolName string
		var currentToolArgs strings.Builder

		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				yield(nil, fmt.Errorf("读取 API 响应流出错: %w", err))
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

			var chunk glmStreamResponse
			if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
				// Ignore unparseable lines safely
				continue
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			delta := choice.Delta

			// Handle streaming Tool Call arguments
			if len(delta.ToolCalls) > 0 {
				tc := delta.ToolCalls[0]
				if tc.Function.Name != "" {
					currentToolName = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					currentToolArgs.WriteString(tc.Function.Arguments)
				}

				if choice.FinishReason == "tool_calls" || choice.FinishReason == "stop" {
					// Finalize tool call chunk
					var parsedArgs map[string]any
					_ = json.Unmarshal([]byte(currentToolArgs.String()), &parsedArgs)

					yield(&model.LLMResponse{
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
					}, nil)
				}
				continue
			}

			// Handle streaming regular text content
			if delta.Content != "" {
				yield(&model.LLMResponse{
					Content: &genai.Content{
						Role: "model",
						Parts: []*genai.Part{
							{Text: delta.Content},
						},
					},
					Partial:      true,
					TurnComplete: false,
				}, nil)
			}

			if choice.FinishReason == "stop" {
				yield(&model.LLMResponse{
					Content: &genai.Content{
						Role: "model",
						Parts: []*genai.Part{
							{Text: ""},
						},
					},
					Partial:      false,
					TurnComplete: true,
				}, nil)
				break
			}
		}
	}
}

// generateSimulatedGLM handles high-fidelity GLM-4 simulation
func (g *GLMAdapter) generateSimulatedGLM(ctx context.Context, req *model.LLMRequest) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		userPrompt := "hello"
		if len(req.Contents) > 0 {
			content := req.Contents[len(req.Contents)-1]
			if len(content.Parts) > 0 {
				if part := content.Parts[0]; part != nil && part.Text != "" {
					userPrompt = part.Text
				}
			}
		}

		hasToolResponse := false
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				if p != nil && p.FunctionResponse != nil {
					hasToolResponse = true
				}
			}
		}

		if hasToolResponse {
			response := "我已经成功通过 GLM-4 智谱引擎运行了您授权的操作，并捕获了返回的控制台输出。请问还有什么我可以协助您的？"
			for i := 0; i < len(response); i += 6 {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(25 * time.Millisecond)
				
				end := i + 6
				if end > len(response) {
					end = len(response)
				}
				
				chunk := response[i:end]
				yield(&model.LLMResponse{
					Content: &genai.Content{
						Role: "model",
						Parts: []*genai.Part{
							{Text: chunk},
						},
					},
					Partial:      true,
					TurnComplete: false,
				}, nil)
			}
			yield(&model.LLMResponse{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{Text: ""},
					},
				},
				Partial:      false,
				TurnComplete: true,
			}, nil)
			return
		}

		// Trigger sensitive tools
		isSensitiveRequest := false
		var toolName string
		var toolArgs map[string]any

		if containsAny(userPrompt, []string{"run", "execute", "go test", "运行", "执行"}) {
			isSensitiveRequest = true
			toolName = "shell_run"
			toolArgs = map[string]any{"command": "go test ./..."}
		} else if containsAny(userPrompt, []string{"write", "create", "make", "写入", "创建"}) {
			isSensitiveRequest = true
			toolName = "file_write"
			toolArgs = map[string]any{"path": "glm_dummy.txt", "content": "hello from simulated GLM-4 agent"}
		}

		if isSensitiveRequest {
			yield(&model.LLMResponse{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{
							FunctionCall: &genai.FunctionCall{
								Name: toolName,
								Args: toolArgs,
							},
						},
					},
				},
				Partial:      false,
				TurnComplete: false,
			}, nil)
			return
		}

		// Standard GLM response
		prefix := fmt.Sprintf("🇨🇳 **[GLM-4 智谱引擎仿真测试]** 您好！我是基于智谱大模型驱动的 go-claude 智能研发助手。\n\n我收到了您的指令：\"%s\"。\n\n", userPrompt)
		body := "智谱大语言模型具备极强的代码检索与多轮敏感命令交互能力。\n\n```go\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, Zhipu GLM-4 with ADK-Go!\")\n}\n```\n\n您可以随时在仿真终端下测试敏感操作：\n- 输入 `运行测试` 触发 `shell_run` 并拦截确认；\n- 输入 `创建文件` 触发 `file_write` 并拦截确认。"
		
		fullResponse := prefix + body
		for i := 0; i < len(fullResponse); i += 6 {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)

			end := i + 6
			if end > len(fullResponse) {
				end = len(fullResponse)
			}

			chunk := fullResponse[i:end]
			yield(&model.LLMResponse{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{Text: chunk},
					},
				},
				Partial:      true,
				TurnComplete: false,
			}, nil)
		}

		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{Text: ""},
				},
			},
			Partial:      false,
			TurnComplete: true,
		}, nil)
	}
}
