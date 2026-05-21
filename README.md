<p align="center">
  <img src="https://img.shields.io/badge/Genkit--powered-Go%20SDK-00ADD8?style=for-the-badge&logo=go" alt="Genkit Powered">
  <img src="https://img.shields.io/badge/TUI-Bubble%20Tea-FF75B5?style=for-the-badge&logo=terminal" alt="Bubble Tea TUI">
</p>

<h1 align="center">🍃 iroha code 🍃</h1>

<p align="center">
  <strong>An interactive, terminal-native AI Coding Agent CLI powered by Google Genkit & Charm TUI</strong>
  <br>
  <em>Designed for developers who love the terminal. Edit files, run shell commands, track tasks, and trace flows locally.</em>
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
  <a href="https://github.com/Planckbaka/iroha-cli/pulls">
    <img src="https://img.shields.io/badge/PRs-welcome-brightgreen.svg" alt="PRs Welcome">
  </a>
</p>

<p align="center">
  <a href="./README_CN.md"><strong>🇨🇳 中文文档</strong></a>
</p>

---

## 🚀 Welcome to Iroha

**Iroha** (compiled as `iroha`, repository `iroha-cli`) is a highly responsive, terminal-native interactive AI coding agent. By bridging **Google Genkit (Go SDK)** for backend LLM orchestration and **Charm's Bubble Tea TUI framework** for the user interface, Iroha provides developers with an intelligent copilot that operates directly in their local repository workspace.

Unlike traditional text-only CLI tools, Iroha features interactive human-in-the-loop approvals, full contextual memory, extensible Hook pipelines, and built-in SWE tools to inspect and modify codebase structures safely.

---

## ✨ Features

- **🍃 Google Genkit Core Engine** — Uses Genkit Go SDK under the hood for unified multi-provider routing (Gemini, Claude, OpenAI, DeepSeek, GLM, etc.) and fully structured parameter parsing.
- **📊 Real-time Trace Observability** — Built-in OpenTelemetry tracing. Run under the Genkit dev server to visual every step (prompt variables, tool calls, and model latencies) in the **Genkit Developer UI** at `localhost:4000`.
- **📟 Premium Bubble Tea TUI** — Terminal UI built with Charm stack (`Bubble Tea`, `Lipgloss`, and `Glamour`) featuring smooth styling, interactive selections, and markdown syntax rendering.
- **🛡️ Human-in-the-Loop Permissions** — Dynamic three-level safety model (`Default` / `Plan` / `Auto`) with interactive user confirmation for sensitive tool invocations (such as writing files or running shell tests).
- **🪝 Extensible Hook Pipelines** — Seamless hook hooks (`PreToolUse` / `PostToolUse` / `SessionStart`) to intercept, blocks, or auto-annotate agent decisions.
- **🧠 Cross-Session Long-term Memory** — Persistent local memory segmented into user preferences, feedback corrections, project facts, and external references that survive terminal restarts.
- **📝 Structured Task Checklist** — Dynamic session-level TODO checklist tracking with proactive nag reminders to prevent the agent from looping or losing track of its goals.

---

## 🛠️ Quick Start

### 1. Install from Source

Make sure you have Go `1.26` or higher installed:

```bash
go install github.com/Planckbaka/iroha-cli/cmd/agent-cli@latest
```

### 2. Download Pre-built Binary

Or directly fetch the compiled executable for macOS, Linux, or Windows from the [Releases page](https://github.com/Planckbaka/iroha-cli/releases).

### 3. Interactive Configuration Wizard

When launching with an online provider for the first time, Iroha launches a terminal configuration wizard automatically:

```bash
# Set provider & model. Naming follows Genkit style
./iroha --provider gemini --model gemini-2.5-flash
```

Or trigger manual configuration at any time:

```bash
./iroha --config
```

> **🔑 Supported Environment Variables:**
> - Gemini: `GEMINI_API_KEY`
> - Claude: `ANTHROPIC_API_KEY`
> - OpenAI: `OPENAI_API_KEY`
> - Zhipu GLM: `ZHIPU_API_KEY`
> - DeepSeek: `DEEPSEEK_API_KEY`

### 4. Fully Offline Simulation Mode

Try out the beautiful terminal interface and human-in-the-loop tool flow instantly without any API keys or network connection:

```bash
./iroha
```

---

## 🔍 Observability & Local Tracing (Genkit Developer UI)

Thanks to the native Google Genkit engine, Iroha supports full telemetry out of the box.

To inspect, debug, and monitor how the AI agent thinks, calls tools, and parses files:

1. Export local dev environment variable:
   ```bash
   export GENKIT_ENV=dev
   ```
2. Start the agent CLI under the Genkit Dev Server hook:
   ```bash
   npx genkit start -- ./iroha
   ```
3. Open **`http://localhost:4000`** in your browser. You will see an interactive dashboard displaying full execution traces, latency timelines, and visual model invocation steps.

---

## 💻 Slash Commands

Inside the interactive chat TUI, prefix your line with `/` to run control commands:

| Command | Description |
|---------|-------------|
| `/mode <default\|plan\|auto>` | Switch active safety permission mode |
| `/rules` | View active path and tool authorization rules |
| `/hooks [reload]` | List current active Hook scripts or reload them |
| `/memory` | Inspect durable project memories and user preferences |
| `/sessions` | Switch between historical persistence sessions in a TUI selection list |
| `/exit` | Safely save memory state and exit |

---

## 🚦 Three-Level Permission Modes

To ensure sensitive operations (like executing shell commands or overwriting configuration files) are safe, Iroha features three execution modes:

- **🛡️ Default Mode**: Requests your explicit inline authorization (`y` to allow, `n` to reject, `a` to trust always for this tool) for all sensitive operations. Includes an offline AI review step (`🤖 ai-review`) verifying command safety beforehand.
- **📖 Plan Mode (Read-only)**: Restricts the agent to reading files, grep searching, and planning. Shell command execution and file modification tools are strictly blocked.
- **🚀 Auto Mode**: Automatically executes commands and writes files silently if the offline AI reviewer marks the operation as safe, minimizing human interruption.

---

## 📂 Architecture Overview

```
                        ┌────────────────────────┐
                        │     cmd/agent-cli      │  <-- CLI Config & Entry
                        └───────────┬────────────┘
                                    │
                        ┌───────────▼────────────┐
                        │       pkg/agent        │  <-- Runner, Memory, Hook, Task
                        └───────────┬────────────┘
                                    │
            ┌───────────────────────┼───────────────────────┐
            ▼                       ▼                       ▼
 ┌────────────────────┐   ┌────────────────────┐   ┌────────────────────┐
 │      pkg/llm       │   │      pkg/tui       │   │     pkg/config     │
 │ (Genkit Go SDK     │   │ (Bubble Tea Event, │   │ (Setup Wizard,     │
 │  Adapter &         │   │  Lipgloss Styles,  │   │  JSON Config)      │
 │  OTel Tracing)     │   │  Markdown Render)  │   │                    │
 └────────────────────┘   └────────────────────┘   └────────────────────┘
```

---

## 🤝 Contributing

We welcome contributions of all kinds! Please read our [Contributing Guide](CONTRIBUTING.md) to set up your local development environment.

We encourage enabling **automerge** on pull requests once continuous integration (CI) tests pass successfully to keep development swift and automated.

---

## 📄 License

This project is licensed under the Apache 2.0 License - see the [LICENSE](LICENSE) file for details.
