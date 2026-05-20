<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# llm

## Purpose
LLM provider abstraction layer. Implements the `model.LLM` interface from Google ADK for Zhipu GLM-4, OpenAI-compatible APIs, and an offline simulation mode.

## Key Files
| File | Description |
|------|-------------|
| `adapter.go` | `ProviderType` enum, `NewAdapter` factory, `SimulatedAdapter` — offline mock with streaming text and tool call simulation |
| `glm.go` | `GLMAdapter` — Zhipu GLM-4 / OpenAI-compatible integration with SSE streaming, tool calls, context compaction (micro-compaction + conversational summarization) |

## For AI Agents

### Working In This Directory
- `NewAdapter` is the entry point — returns a `model.LLM` based on provider type
- `GLMAdapter` handles SSE parsing for streaming responses
- `CompactRequestContents` implements two-level context compaction:
  1. Large tool outputs (>1000 bytes) archived to `~/.go-claude/transcripts/`
  2. Conversations >12 rounds summarized with compacted middle section
- `SimulatedAdapter` provides a fully offline demo mode
- Decoupled callbacks (`NagReminderTrigger`, `NoteRoundWithoutUpdate`) prevent circular deps with `pkg/agent`

### Testing Requirements
- `go test ./pkg/llm/...`
- Tests exist for: glm adapter

### Common Patterns
- `iter.Seq2[*model.LLMResponse, error]` for streaming (Go 1.26 iterator pattern)
- GLM uses OpenAI-compatible chat completions API format
- Role mapping: ADK `"model"` → `"assistant"` for GLM API

## Dependencies

### Internal
- `pkg/agent` (indirect via callbacks only — `NagReminderTrigger`, `NoteRoundWithoutUpdate`)

### External
- `google.golang.org/adk/model` — LLM interface
- `google.golang.org/genai` — Content/Part/FunctionCall types

<!-- MANUAL: -->
