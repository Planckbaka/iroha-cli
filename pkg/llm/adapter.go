package llm

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// ProviderType represents the LLM provider
type ProviderType string

const (
	ProviderGemini   ProviderType = "gemini"
	ProviderClaude   ProviderType = "claude"
	ProviderOpenAI   ProviderType = "openai"
	ProviderGLM      ProviderType = "glm"
	ProviderSimulate ProviderType = "simulate"
)

// NewAdapter creates a new model.LLM based on the provider, model name, apiKey, and optional baseURL
func NewAdapter(provider ProviderType, modelName string, apiKey string, baseURL string) (model.LLM, error) {
	switch provider {
	case ProviderSimulate:
		return NewSimulatedAdapter(modelName), nil
	case ProviderGemini:
		// Wrap Gemini initialization. We can use the mock or real client here.
		return NewSimulatedAdapter("gemini-2.5-flash-simulated"), nil
	case ProviderGLM, ProviderOpenAI:
		return NewGLMAdapter(modelName, apiKey, baseURL), nil
	default:
		return NewSimulatedAdapter(string(provider) + "-" + modelName + "-simulated"), nil
	}
}

// SimulatedAdapter is an offline mock LLM that generates streaming text and tool calls.
// This ensures that the CLI has a premium, immediately working demo out of the box.
type SimulatedAdapter struct {
	modelName string
}

func NewSimulatedAdapter(name string) *SimulatedAdapter {
	return &SimulatedAdapter{modelName: name}
}

func (s *SimulatedAdapter) Name() string {
	return s.modelName
}

func (s *SimulatedAdapter) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		// Build dynamic system prompt
		var systemPrompt string
		if SystemPromptTrigger != nil {
			systemPrompt = SystemPromptTrigger()
		} else if req.Config != nil && req.Config.SystemInstruction != nil {
			var parts []string
			for _, p := range req.Config.SystemInstruction.Parts {
				if p.Text != "" {
					parts = append(parts, p.Text)
				}
			}
			systemPrompt = strings.Join(parts, "\n")
		}
		_ = systemPrompt

		// Extract user prompt from the last content
		userPrompt := "hello"
		if len(req.Contents) > 0 {
			content := req.Contents[len(req.Contents)-1]
			if len(content.Parts) > 0 {
				if part := content.Parts[0]; part != nil && part.Text != "" {
					userPrompt = part.Text
				}
			}
		}

		// Check if we already have a tool response in the conversation history
		hasToolResponse := false
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				if p != nil && p.FunctionResponse != nil {
					hasToolResponse = true
				}
			}
		}

		// If there is already a tool response, finalize the agent's turn with an explanation
		if hasToolResponse {
			response := "我已经成功运行了您授权的敏感操作，并且得到了预期的运行结果。请问还有什么我可以帮您的？"
			for i := 0; i < len(response); i += 6 {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(30 * time.Millisecond)

				end := i + 6
				if end > len(response) {
					end = len(response)
				}

				chunk := response[i:end]
				if !yield(&model.LLMResponse{
					Content: &genai.Content{
						Role: "model",
						Parts: []*genai.Part{
							{Text: chunk},
						},
					},
					Partial:      true,
					TurnComplete: false,
				}, nil) {
					return
				}
			}
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
			return
		}

		// Simulate tool calling for commands like "run" or "write"
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
			toolArgs = map[string]any{"path": "dummy.txt", "content": "hello from simulated agent"}
		}

		if isSensitiveRequest {
			// Trigger a function call (tool call) response
			if !yield(&model.LLMResponse{
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
			}, nil) {
				return
			}
			return
		}

		// Standard streaming text response
		prefix := fmt.Sprintf("[%s] 您好！我是 go-claude CLI 助手。我收到了您的输入：\"%s\"。\n\n这里是为您实时流式生成的思考过程：\n\n", s.modelName, userPrompt)
		body := "1. **理解意图**：用户正在测试 CLI 终端的功能。\n2. **执行规划**：展示平滑的流式输出，高亮显示 Lipgloss 样式和 Glamour 语法渲染。\n3. **输出生成**：流式传输此响应。\n\n```go\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, modern interactive CLI Agent!\")\n}\n```\n\n您可以尝试输入 `运行测试` 或是 `创建文件` 来查看**敏感工具的三阶段人机确认交互机制**！"

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
			if !yield(&model.LLMResponse{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{Text: chunk},
					},
				},
				Partial:      true,
				TurnComplete: false,
			}, nil) {
				return
			}
		}

		// Final empty chunk to signal completion
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

func containsAny(s string, keywords []string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
