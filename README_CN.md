<p align="center">
  <img src="https://img.shields.io/badge/Genkit--powered-Go%20SDK-00ADD8?style=for-the-badge&logo=go" alt="Genkit Powered">
  <img src="https://img.shields.io/badge/TUI-Bubble%20Tea-FF75B5?style=for-the-badge&logo=terminal" alt="Bubble Tea TUI">
</p>

<h1 align="center">🍃 iroha code 🍃</h1>

<p align="center">
  <strong>基于 Google Genkit 与 Charm TUI 构建的终端原生交互式 AI 智能体 (Agent) 命令行工具</strong>
  <br>
  <em>专为极客与终端爱好者设计。编辑文件、运行 Shell 命令、跟踪规划进度、本地流式追踪分析。</em>
</p>

<p align="center">
  <a href="https://github.com/Planckbaka/iroha-cli/actions/workflows/ci.yml">
    <img src="https://github.com/Planckbaka/iroha-cli/actions/workflows/ci.yml/badge.svg" alt="CI">
  </a>
  <a href="https://go.dev/">
    <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go" alt="Go 版本">
  </a>
  <a href="LICENSE">
    <img src="https://img.shields.io/badge/许可证-Apache--2.0-blue" alt="许可证">
  </a>
  <a href="https://github.com/Planckbaka/iroha-cli/pulls">
    <img src="https://img.shields.io/badge/PRs-welcome-brightgreen.svg" alt="PRs Welcome">
  </a>
</p>

<p align="center">
  <a href="./README.md"><strong>🇺🇸 English Documentation</strong></a>
</p>

---

## 🚀 欢迎使用 Iroha

**Iroha**（编译模块名为 `iroha`，GitHub 仓库名为 `iroha-cli`）是一个响应极快、终端原生的交互式 AI 编码智能体（Coding Agent）。项目底层由 **Google Genkit (Go SDK)** 提供大模型调度与链路流式追踪支持，上层基于 **Charm Bubble Tea 终端 UI 框架** 构建精美的人机交互界面，旨在为您提供位于本地代码仓库的高效智能编程副驾驶。

与传统的纯文本 CLI 相比，Iroha 具备人性化的人机确认机制（Human-in-the-Loop）、完备的上下文持久化记忆、可扩展的 Hook 管线以及内置的流式软件工程工具集，能在保障安全的前提下帮助您敏捷重构代码。

---

## ✨ 功能特性

- **🍃 Google Genkit 核心驱动** — 采用 Genkit Go SDK 整合各大提供商模型（Gemini、Claude、OpenAI、DeepSeek、智谱 GLM 等），提供高强度的参数规范化与插件化模型路由。
- **📊 实时流式链路追踪 (Observability)** — 零额外开销整合 OpenTelemetry 标准。通过 Genkit 调试服务启动，即可在本地 **Genkit Developer UI**（`localhost:4000`）中清晰审查 prompt 拼装、工具调用参数以及模型延迟明细。
- **📟 精美 Bubble Tea 终端 UI** — 基于 Charm 栈构建的流式 UI，支持 HSL 渐变配色、终端尺寸自适应、交互式列表选择器以及 Markdown 语法高亮渲染。
- **🛡️ 人机确认权限机制** — 内置三级安全执行模式（`默认` / `规划` / `自动`）。任何敏感工具调用（如写入文件或执行 shell 命令）前均会请求用户进行行内交互式授权，并包含本地 AI 自动审查保护。
- **🪝 可扩展的 Hook 管线** — 拥有完整的 Hook 系统（`PreToolUse` / `PostToolUse` / `SessionStart`），允许您通过本地脚本动态阻断、注入或修改智能体的执行流。
- **🧠 跨会话长期记忆** — 分层设计长期文件记忆（用户偏好、更正反馈、项目事实、外部引用），实现“即使关闭终端，记忆依然保留”。
- **📝 结构化任务规划 (TODO)** — 会话级任务列表，配合强制任务打卡提醒（Nag Reminder），彻底杜绝智能体在漫长且复杂的修改步骤中“迷路”或产生死循环。

---

## 🛠️ 快速开始

### 1. 一键全自动安装 (macOS & Linux)

使用以下全自动脚本，即刻完成系统平台与 CPU 架构的检测、最新版本下载及自动安装：

```bash
curl -fsSL https://raw.githubusercontent.com/Planckbaka/iroha-cli/main/install.sh | sh
```

### 2. 通过 Homebrew 安装

或者，将我们的 tap 添加到 Homebrew 并一键完成部署：

```bash
brew tap Planckbaka/iroha-cli
brew install iroha
```

### 3. 从源码编译安装

确保您的本地环境已安装 Go `1.26` 或更高版本：

```bash
go install github.com/Planckbaka/iroha-cli/cmd/agent-cli@latest
```

### 3. 交互式配置向导

首次使用在线模型时，Iroha 会自动为您引导启动配置向导：

```bash
# 指定模型提供商与模型名称，遵循 Genkit 插件路由规范
./iroha --provider gemini --model gemini-2.5-flash
```

或者随时输入以下命令进行手动配置与更新：

```bash
./iroha --config
```

> **🔑 支持的环境变量：**
> - Gemini: `GEMINI_API_KEY`
> - Claude: `ANTHROPIC_API_KEY`
> - OpenAI: `OPENAI_API_KEY`
> - 智谱 GLM-4: `ZHIPU_API_KEY`
> - DeepSeek: `DEEPSEEK_API_KEY`

### 4. 离线仿真运行

如果您无需配置任何 API Key，也想立刻上手体验精美界面与确认流程，可以直接运行仿真模式：

```bash
./iroha
```

---

## 🔍 链路追踪与可视化调试 (Genkit Developer UI)

得益于 Google Genkit 原生引擎支持，Iroha 拥有卓越的 Telemetry 链路追踪能力。

若要在本地直观洞察 AI 智能体的思维链、上下文检索与工具执行流：

1. 导出本地开发环境变量：
   ```bash
   export GENKIT_ENV=dev
   ```
2. 在 Genkit Dev 服务挂载下启动 Agent 客户端：
   ```bash
   npx genkit start -- ./iroha
   ```
3. 在浏览器中打开 **`http://localhost:4000`**。您将看到一个实时渲染的流式可观测仪表盘，展示每一步 tool_call、耗时甘特图及完整的历史 Trace 数据。

---

## 💻 斜杠命令

在 TUI 聊天输入框内，以 `/` 开头运行控制指令：

| 斜杠命令 | 功能说明 |
|---------|---------|
| `/mode <default\|plan\|auto>` | 动态切换安全权限执行模式 |
| `/rules` | 打印并审查当前已授权或拦截的路径/工具安全规则 |
| `/hooks [reload]` | 查看当前活动的 Hook 脚本，或即时重新加载 Hook 配置 |
| `/memory` | 查看持久化项目记忆、更正事实及用户偏好 |
| `/sessions` | 打开历史会话交互式选择器，无缝载入或分支（Fork）历史上下文 |
| `/exit` | 安全保存持久化记忆与 Session 状态并退出程序 |

---

## 🚦 三级权限执行模式

为了确保系统级敏感操作（如运行随机 shell 代码、重构核心包配置文件）的绝对安全，Iroha 划分了三种模式：

- **🛡️ 默认模式 (Default)**：对所有写文件、读目录、运行 shell 工具的操作，前置进行行内用户确认（输入 `y` 授权，`n` 拒绝，`a` 设为该会话此工具的白名单规则）。在确认前会前置运行 `🤖 ai-review` 离线代码安全评估。
- **📖 规划模式 (Plan)**：严格只读模式。Agent 只能读取文件、检索 grep、进行逻辑演算，所有涉及修改或执行的工具调用都会被底层安全熔断直接拒绝。
- **🚀 自动模式 (Auto)**：高吞吐量无人值守模式。若离线安全评估系统将该命令标记为“100% 安全”，Agent 将自动静默执行，最大限度减少人机打扰。

---

## 📂 架构简图

```
                        ┌────────────────────────┐
                        │     cmd/agent-cli      │  <-- CLI 配置与程序入口
                        └───────────┬────────────┘
                                    │
                        ┌───────────▼────────────┐
                        │       pkg/agent        │  <-- 运行器、记忆层、Hook、任务规划
                        └───────────┬────────────┘
                                    │
            ┌───────────────────────┼───────────────────────┐
            ▼                       ▼                       ▼
 ┌────────────────────┐   ┌────────────────────┐   ┌────────────────────┐
 │      pkg/llm       │   │      pkg/tui       │   │     pkg/config     │
 │ (Genkit Go SDK     │   │ (Bubble Tea 事件环、│   │ (向导生成器、      │
 │  适配层 & 链路追踪) │   │  Lipgloss 配色渲染、│   │  JSON 读写器)       │
 └────────────────────┘   │  Markdown 解析渲染) │   └────────────────────┘
                          └────────────────────┘
```

---

## 🤝 参与贡献

我们极其欢迎任何形式的 Pull Request 与 Issue！请通读我们的 [贡献指南 (CONTRIBUTING.md)](CONTRIBUTING.md) 以快速搭建您的本地 TUI 调试与测试环境。

本仓库支持并在 CI 单元测试通过后鼓励使用 **Automerge (自动合并)**，以保证项目敏捷的开源迭代节奏。

---

## 📄 开源许可证

本项目基于 Apache 2.0 开源许可证发布 - 详情参见 [LICENSE](LICENSE) 文件。
