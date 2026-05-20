<p align="center">
  <strong>go-claude</strong>
  <br>
  <em>基于智谱 GLM-4 / OpenAI 的交互式 AI 智能体命令行工具</em>
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
</p>

<p align="center">
  <a href="./README.md">English</a>
</p>

---

> **命名说明：** 项目模块名为 **go-claude**，GitHub 仓库为 **iroha-cli**，编译后的二进制文件为 **agent-cli**。它们指的是同一个项目。

## 功能特性

- **精美终端界面** — 基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea)、[Lipgloss](https://github.com/charmbracelet/lipgloss) 和 [Glamour](https://github.com/charmbracelet/glamour) 构建的高品质交互式终端 UI。
- **多模型提供商** — 可插拔适配器，支持智谱 GLM-4、OpenAI 兼容 API，以及完全离线的仿真模式。
- **工具调用** — 内置软件工程工具：`file_read`、`file_write`、`search_grep`、`shell_run`，支持流式执行。
- **人机确认权限** — 三级安全模型（默认/规划/自动），敏感操作需经用户内联确认。
- **Hook 管线** — 可扩展的 Hook 系统（`PreToolUse` / `PostToolUse` / `SessionStart`），支持阻断/注入/继续语义。
- **跨会话记忆** — 基于文件的持久化记忆系统，支持四种类型（用户偏好/反馈更正/项目事实/外部引用），重启后依然保留。
- **任务规划** — 会话级任务管理，配合提醒机制确保复杂多步骤工作保持在轨道上。

## 快速开始

### 从源码安装

```bash
go install github.com/Planckbaka/iroha-cli/cmd/agent-cli@latest
```

### 下载二进制文件

从 [Releases 页面](https://github.com/Planckbaka/iroha-cli/releases) 下载适合您平台的最新版本。

### 配置

首次使用在线提供商时，交互式配置向导会自动启动：

```bash
agent-cli --provider glm --model glm-4
```

或手动配置：

```bash
agent-cli --config
```

支持的环境变量：`ZHIPU_API_KEY`、`OPENAI_API_KEY`、`ANTHROPIC_API_KEY`、`GEMINI_API_KEY`

### 离线体验

无需 API Key — 以仿真模式运行：

```bash
agent-cli
```

### 斜杠命令

| 命令 | 说明 |
|------|------|
| `/mode <plan\|auto\|default>` | 切换权限模式 |
| `/rules` | 查看当前权限规则 |
| `/hooks [reload]` | 查看或重载 Hook 配置 |
| `/memory` | 查看持久化记忆 |
| `/exit` | 退出 |

## 架构

```
┌─────────────────────┐
│   cmd/agent-cli     │  入口 & 配置解析
└──────────┬──────────┘
           │
    ┌──────┴──────┐
    │   pkg/agent │  运行器、工具、权限、Hook、记忆、任务规划
    └──────┬──────┘
           │
    ┌──────┼──────┬──────────┐
    ▼      ▼      ▼          ▼
pkg/llm  pkg/tui pkg/config
(适配器, (Bubble   (向导,
 GLM-4,   Tea      JSON)
 OpenAI,  模型,
 仿真)   样式)
```

## 演示

<!-- 在此处添加 TUI 截图 -->
```
  go-claude AI Agent CLI (v1.3.0)
  Model: glm-4 | Mode: default | Session: session-default

  Use Up/Down to cycle history. Type /exit or Ctrl+C to quit.
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  > 帮我重构认证模块

  🔨 [shell_run] 正在尝试运行命令: $ go test ./pkg/auth/...
     ⚠️  Security Gate warning: shell_metachar
     是否授权执行此操作？ (y/n/a)
```

## 社区

- [贡献指南](.github/CONTRIBUTING.md)
- [行为准则](.github/CODE_OF_CONDUCT.md)
- [安全策略](.github/SECURITY.md)
- [支持](.github/SUPPORT.md)

## 许可证

[Apache-2.0](LICENSE)
