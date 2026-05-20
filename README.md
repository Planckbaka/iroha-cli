<p align="center">
  <strong>go-claude</strong>
  <br>
  <em>An interactive AI agent CLI powered by Zhipu GLM-4 / OpenAI</em>
</p>

<p align="center">
  <a href="https://github.com/Planckbaka/iroha-cli/actions/workflows/ci.yml">
    <img src="https://github.com/Planckbaka/iroha-cli/actions/workflows/ci.yml/badge.svg" alt="CI">
  </a>
  <a href="https://go.dev/">
    <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go" alt="Go Version">
  </a>
  <a href="LICENSE">
    <img src="https://img.shields.io/badge/License-Apache--2.0-blue" alt="License">
  </a>
</p>

<p align="center">
  <a href="./README_CN.md">中文文档</a>
</p>

---

> **Naming note:** The project module is **go-claude**, the GitHub repository is **iroha-cli**, and the compiled binary is **agent-cli**. They all refer to the same project.

## Features

- **Beautiful TUI** — Terminal UI built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lipgloss](https://github.com/charmbracelet/lipgloss), and [Glamour](https://github.com/charmbracelet/glamour) for a premium interactive experience.
- **Multi-Provider LLM** — Pluggable adapter supporting Zhipu GLM-4, OpenAI-compatible APIs, and a fully offline simulation mode.
- **Tool Use** — Built-in SWE tools: `file_read`, `file_write`, `search_grep`, `shell_run` with streaming execution.
- **Human-in-the-Loop Permissions** — Three-level security model (default / plan / auto) with inline confirmation prompts for sensitive operations.
- **Hook Pipeline** — Extensible hook system (`PreToolUse` / `PostToolUse` / `SessionStart`) with block/inject/continue semantics.
- **Cross-Session Memory** — Persistent file-based memory with four types (user, feedback, project, reference) that survives restarts.
- **Task Planning** — Session-level todo management with nag reminders to keep complex multi-step work on track.

## Quick Start

### Install from Source

```bash
go install github.com/Planckbaka/iroha-cli/cmd/agent-cli@latest
```

### Download Binary

Download the latest release for your platform from the [Releases page](https://github.com/Planckbaka/iroha-cli/releases).

### Configure

On first run with an online provider, the interactive setup wizard launches automatically:

```bash
agent-cli --provider glm --model glm-4
```

Or configure manually:

```bash
agent-cli --config
```

Supported environment variables: `ZHIPU_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`

### Offline Demo

No API key needed — run in simulation mode:

```bash
agent-cli
```

### Slash Commands

| Command | Description |
|---------|-------------|
| `/mode <plan\|auto\|default>` | Switch permission mode |
| `/rules` | List active permission rules |
| `/hooks [reload]` | View or reload hook configuration |
| `/memory` | View persistent memories |
| `/exit` | Quit |

## Architecture

```
┌─────────────────────┐
│   cmd/agent-cli     │  Entry point & config resolution
└──────────┬──────────┘
           │
    ┌──────┴──────┐
    │   pkg/agent │  Runner, tools, permissions, hooks, memory, todo
    └──────┬──────┘
           │
    ┌──────┼──────┬──────────┐
    ▼      ▼      ▼          ▼
pkg/llm  pkg/tui pkg/config
(Adapter, (Bubble   (Wizard,
 GLM-4,   Tea      JSON)
 OpenAI,  Model,
 Sim)     Styles)
```

## Demo

<!-- Add TUI screenshots here -->
```
  go-claude AI Agent CLI (v1.3.0)
  Model: glm-4 | Mode: default | Session: session-default

  Use Up/Down to cycle history. Type /exit or Ctrl+C to quit.
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  > help me refactor the authentication module

  🔨 [shell_run] 正在尝试运行命令: $ go test ./pkg/auth/...
     ⚠️  Security Gate warning: shell_metachar
     是否授权执行此操作？ (y/n/a)
```

## Community

- [Contributing Guide](.github/CONTRIBUTING.md)
- [Code of Conduct](.github/CODE_OF_CONDUCT.md)
- [Security Policy](.github/SECURITY.md)
- [Support](.github/SUPPORT.md)

## License

[Apache-2.0](LICENSE)
