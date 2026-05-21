package llm

import (
	"context"
	"encoding/json"
	"iter"
	"strings"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// GenkitModelAdapter bridges the Google Genkit Go SDK into the Google ADK model.LLM interface.
type GenkitModelAdapter struct {
	g                *genkit.Genkit
	modelName        string
	systemPrompt     string
	hooks            AdapterHooks
	cumulativeTokens int
}

// NewGenkitModelAdapter creates a new GenkitModelAdapter instance.
func NewGenkitModelAdapter(g *genkit.Genkit, modelName string, systemPrompt string, hooks AdapterHooks) *GenkitModelAdapter {
	return &GenkitModelAdapter{
		g:            g,
		modelName:    modelName,
		systemPrompt: systemPrompt,
		hooks:        hooks,
	}
}

// Name returns the active Genkit model name.
func (m *GenkitModelAdapter) Name() string {
	return m.modelName
}

// CumulativeTokens implements the TokenTracker interface.
func (m *GenkitModelAdapter) CumulativeTokens() int {
	return m.cumulativeTokens
}

// AddTokens adds to the token tracker count.
func (m *GenkitModelAdapter) AddTokens(n int) {
	m.cumulativeTokens += n
}

// GenerateContent maps ADK LLMRequest to Genkit options, invokes genkit generation, and yields chunks back.
func (m *GenkitModelAdapter) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.hooks != nil {
			m.hooks.NoteRound()
		}

		compactedContents := req.Contents

		// Inject Nag Reminder if triggered
		if m.hooks != nil {
			nagMsg := m.hooks.NagReminder()
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

		// Build dynamic system prompt
		var systemPrompt string
		if m.systemPrompt != "" {
			systemPrompt = m.systemPrompt
		} else if req.Config != nil && req.Config.SystemInstruction != nil {
			var parts []string
			for _, p := range req.Config.SystemInstruction.Parts {
				if p.Text != "" {
					parts = append(parts, p.Text)
				}
			}
			systemPrompt = strings.Join(parts, "\n")
		}

		// Map ADK genai.Content structures to Genkit ai.Message list
		var messages []*ai.Message
		if systemPrompt != "" {
			messages = append(messages, ai.NewSystemMessage(ai.NewTextPart(systemPrompt)))
		}

		for _, c := range compactedContents {
			role := ai.RoleUser
			switch c.Role {
			case "user":
				role = ai.RoleUser
			case "model", "assistant":
				role = ai.RoleModel
			case "system":
				role = ai.RoleSystem
			case "tool", "function":
				role = ai.RoleTool
			}

			var parts []*ai.Part
			for _, p := range c.Parts {
				if p == nil {
					continue
				}
				if p.Text != "" {
					parts = append(parts, ai.NewTextPart(p.Text))
				}
				if p.FunctionCall != nil {
					parts = append(parts, ai.NewToolRequestPart(&ai.ToolRequest{
						Name:  p.FunctionCall.Name,
						Input: p.FunctionCall.Args,
						Ref:   "call_" + p.FunctionCall.Name,
					}))
				}
				if p.FunctionResponse != nil {
					parts = append(parts, ai.NewToolResponsePart(&ai.ToolResponse{
						Name:   p.FunctionResponse.Name,
						Output: p.FunctionResponse.Response,
						Ref:    "call_" + p.FunctionResponse.Name,
					}))
				}
			}

			messages = append(messages, ai.NewMessage(role, nil, parts...))
		}

		// Build generate options
		var opts []ai.GenerateOption
		opts = append(opts, ai.WithModelName(m.modelName))
		opts = append(opts, ai.WithMessages(messages...))

		// Map generation configurations
		if req.Config != nil {
			var commonConfig ai.GenerationCommonConfig
			if req.Config.Temperature != nil {
				commonConfig.Temperature = float64(*req.Config.Temperature)
			}
			if req.Config.MaxOutputTokens != 0 {
				commonConfig.MaxOutputTokens = int(req.Config.MaxOutputTokens)
			}
			commonConfig.StopSequences = req.Config.StopSequences
			if req.Config.TopK != nil {
				commonConfig.TopK = int(*req.Config.TopK)
			}
			if req.Config.TopP != nil {
				commonConfig.TopP = float64(*req.Config.TopP)
			}
			opts = append(opts, ai.WithConfig(&commonConfig))

			// Map tools dynamically
			if len(req.Config.Tools) > 0 {
				var toolRefs []ai.ToolRef
				for _, t := range req.Config.Tools {
					if t != nil && len(t.FunctionDeclarations) > 0 {
						for _, fd := range t.FunctionDeclarations {
							if fd != nil {
								var paramsSchema map[string]any
								if fd.ParametersJsonSchema != nil {
									if mSchema, ok := fd.ParametersJsonSchema.(map[string]any); ok {
										paramsSchema = mSchema
									} else {
										bytes, err := json.Marshal(fd.ParametersJsonSchema)
										if err == nil {
											_ = json.Unmarshal(bytes, &paramsSchema)
										}
									}
								}
								// Define a schema-decl tool wrapper since ADK runner handles execution
								toolDef := ai.NewTool[any, any](
									fd.Name,
									fd.Description,
									func(ctx *ai.ToolContext, input any) (any, error) {
										return nil, nil
									},
									ai.WithInputSchema(paramsSchema),
								)
								toolRefs = append(toolRefs, toolDef)
							}
						}
					}
				}
				if len(toolRefs) > 0 {
					opts = append(opts, ai.WithTools(toolRefs...))
				}
			}
		}

		if stream {
			// Streaming Generation
			streamVal := genkit.GenerateStream(ctx, m.g, opts...)
			var currentToolName string
			var currentToolArgs strings.Builder
			var sentFinal bool

			for val, err := range streamVal {
				if err != nil {
					yield(nil, err)
					return
				}

				if val.Done {
					if val.Response != nil {
						if val.Response.Usage != nil {
							m.AddTokens(val.Response.Usage.TotalTokens)
						}

						trs := val.Response.ToolRequests()
						if len(trs) > 0 {
							for _, tr := range trs {
								var parsedArgs map[string]any
								if tr.Input != nil {
									bytes, err := json.Marshal(tr.Input)
									if err == nil {
										_ = json.Unmarshal(bytes, &parsedArgs)
									}
								}
								if !yield(&model.LLMResponse{
									Content: &genai.Content{
										Role: "model",
										Parts: []*genai.Part{
											{
												FunctionCall: &genai.FunctionCall{
													Name: tr.Name,
													Args: parsedArgs,
												},
											},
										},
									},
									Partial:      false,
									TurnComplete: true,
								}, nil) {
									return
								}
								sentFinal = true
							}
						}
					}
					break
				}

				chunk := val.Chunk
				if chunk == nil {
					continue
				}

				text := chunk.Text()
				if text != "" {
					if !yield(&model.LLMResponse{
						Content: &genai.Content{
							Role: "model",
							Parts: []*genai.Part{
								{Text: text},
							},
						},
						Partial:      true,
						TurnComplete: false,
					}, nil) {
						return
					}
				}

				for _, p := range chunk.Content {
					if p != nil && p.IsToolRequest() && p.ToolRequest != nil {
						tr := p.ToolRequest
						if tr.Name != "" {
							currentToolName = tr.Name
						}
						if tr.Input != nil {
							if strInput, ok := tr.Input.(string); ok {
								currentToolArgs.WriteString(strInput)
							} else {
								bytes, _ := json.Marshal(tr.Input)
								currentToolArgs.Write(bytes)
							}
						}
					}
				}
			}

			if currentToolName != "" && !sentFinal {
				var parsedArgs map[string]any
				_ = json.Unmarshal([]byte(currentToolArgs.String()), &parsedArgs)
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
					TurnComplete: true,
				}, nil) {
					return
				}
				sentFinal = true
			}

			if !sentFinal {
				_ = yield(&model.LLMResponse{
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

		} else {
			// Non-streaming Generation
			resp, err := genkit.Generate(ctx, m.g, opts...)
			if err != nil {
				yield(nil, err)
				return
			}

			if resp.Usage != nil {
				m.AddTokens(resp.Usage.TotalTokens)
			}

			var parts []*genai.Part
			text := resp.Text()
			if text != "" {
				parts = append(parts, &genai.Part{Text: text})
			}

			trs := resp.ToolRequests()
			for _, tr := range trs {
				var parsedArgs map[string]any
				if tr.Input != nil {
					bytes, err := json.Marshal(tr.Input)
					if err == nil {
						_ = json.Unmarshal(bytes, &parsedArgs)
					}
				}
				parts = append(parts, &genai.Part{
					FunctionCall: &genai.FunctionCall{
						Name: tr.Name,
						Args: parsedArgs,
					},
				})
			}

			if len(parts) == 0 {
				parts = append(parts, &genai.Part{Text: ""})
			}

			_ = yield(&model.LLMResponse{
				Content: &genai.Content{
					Role:  "model",
					Parts: parts,
				},
				Partial:      false,
				TurnComplete: true,
			}, nil)
		}
	}
}
