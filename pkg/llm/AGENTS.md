<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-23 | Updated: 2026-05-23 -->

# llm

## Purpose
LLM provider abstraction layer. Implements the `model.LLM` interface from Google ADK for 7 providers via 3 adapters: OpenAI-compatible SSE (GLM, OpenAI, DeepSeek, Kimi, SiliconFlow), Anthropic Messages API, and Firebase Genkit SDK (Gemini, Claude SDK).

## Key Files
| File | Description |
|------|-------------|
| `adapter.go` | `ProviderType` enum (7 providers), `NewAdapter` factory, `APIFormat` enum (openai/anthropic) |
| `openai.go` | `OpenAICompatibleAdapter` — HTTP SSE streaming for GLM, OpenAI, DeepSeek, Kimi, SiliconFlow; handles tool call accumulation by index, retry with exponential backoff + jitter |
| `anthropic.go` | `AnthropicAdapter` — HTTP SSE streaming for Anthropic Messages API; `tool_use`/`tool_result` block handling, atomic ID generation |
| `genkit_adapter.go` | `GenkitModelAdapter` — bridges Firebase Genkit Go SDK into ADK `model.LLM` for Gemini and official Claude SDK |
| `helpers.go` | `CollectStream` — non-streaming helper that drains an iterator into a slice |
| `debuglog.go` | `/tmp` debug log for adapter tracing (enabled via env var) |

## For AI Agents

### Working In This Directory
- `NewAdapter` is the entry point — returns a `model.LLM` based on provider type and API format
- Each adapter implements `GenerateContent() -> iter.Seq2[*model.LLMResponse, error]` (Go 1.26 iterator)
- OpenAI adapter: SSE parsing, tool call accumulation by index, retry with backoff
- Anthropic adapter: Proper `tool_use`/`tool_result` content block handling
- Genkit adapter: Bridges Firebase Genkit SDK model actions into ADK interface
- Decoupled callbacks (`NagReminderTrigger`, `NoteRoundWithoutUpdate`, `SystemPromptTrigger`) prevent circular deps with `pkg/agent`
- API format is configurable per-provider (openai vs anthropic protocol)

### Testing Requirements
- `go test ./pkg/llm/...`
- Tests exist for: anthropic adapter (271 lines, httptest SSE mock)
- **Gap**: `glm_test.go` is empty — OpenAI adapter has no test coverage
- **Gap**: No tests for Genkit adapter

### Common Patterns
- `iter.Seq2[*model.LLMResponse, error]` for streaming (Go 1.26 iterator pattern)
- SSE line parsing with `bufio.Scanner`
- Role mapping: ADK `"model"` → provider-specific role names
- Error wrapping with `fmt.Errorf("context: %w", err)`
- Provider defaults (model, base URL, env key) defined in adapter factory

## Dependencies

### Internal
- `pkg/agent` (indirect via callbacks only — `NagReminderTrigger`, `NoteRoundWithoutUpdate`)

### External
- `google.golang.org/adk/model` — LLM interface
- `google.golang.org/genai` — Content/Part/FunctionCall types
- `github.com/firebase/genkit/go` — Genkit Go SDK (for Gemini/Claude)
- `github.com/firebase/genkit/go/plugins/googleai` — Google AI plugin

<!-- MANUAL: -->
