# Contributing to iroha-cli

感谢您有兴趣参与 **iroha-cli** (iroha code) 的建设！这是一个由社区驱动的交互式 AI 智能体终端工具。本指南将帮助您快速搭建本地开发环境、了解项目架构，并规范地提交您的贡献。

---

## 🚀 快速上手本地开发

### 1. 开发环境要求
- **Go**: v1.26.1 或更高版本
- **Git**: 用于版本控制和组件拉取
- 一个支持完整 ANSI 转义序列和 Unicode 的现代终端（如 macOS Terminal, iTerm2, Alacritty, Windows Terminal 等）

### 2. 获取源码与依赖
```bash
git clone https://github.com/Planckbaka/iroha-cli.git
cd iroha-cli
go mod tidy
```

### 3. 本地编译与运行
- **本地编译**：
  ```bash
  go build -o iroha ./cmd/agent-cli
  ```
- **离线模拟运行（无需 API Key）**：
  在开发调试 TUI 界面或修改逻辑时，建议运行在 **Simulate 模式** 下，这样无需联网且免去消耗 API Token：
  ```bash
  ./iroha --provider simulate
  ```
- **使用智谱 GLM API 调试**：
  ```bash
  export ZHIPU_API_KEY="your_api_key_here"
  ./iroha --provider glm
  ```

---

## 📂 项目模块结构

在开发前，请熟悉以下目录划分，以确保代码写在正确的位置：

- [**`cmd/agent-cli/`**](file:///Users/akiwayne/Documents/Project2026/go-project/go-claude/cmd/agent-cli): 应用程序的入口点（`main.go`），负责命令行 Flags 参数解析、配置优先级组合以及初始化启动。
- [**`pkg/config/`**](file:///Users/akiwayne/Documents/Project2026/go-project/go-claude/pkg/config): 负责读取、更新和存储本地配置文件 `~/.iroha.json`，并包含命令行交互式配置向导（Wizard）。
- [**`pkg/llm/`**](file:///Users/akiwayne/Documents/Project2026/go-project/go-claude/pkg/llm): LLM 适配层，支持智谱 GLM-4 官方 API、OpenAI 兼容 API 以及离线仿真器的流式/非流式接入。
- [**`pkg/agent/`**](file:///Users/akiwayne/Documents/Project2026/go-project/go-claude/pkg/agent): 核心 Agent 运行时逻辑。包含工具注册、三级权限模式管理（Plan/Default/Auto）、AI 安全审查、跨会话长期记忆（Memory）、Durable Work Graph 任务规划等。
- [**`pkg/tui/`**](file:///Users/akiwayne/Documents/Project2026/go-project/go-claude/pkg/tui): 基于 Charm 栈构建的 TUI 终端交互系统。使用 Bubble Tea 做事件环、Lipgloss 做渲染排版、Glamour 做 Markdown 块输出。

---

## 🛠️ TUI 调试技巧（重要）

由于 `Bubble Tea` 会接管终端的输入输出并开启 Alternate Screen，**在代码中直接使用 `fmt.Println` 或 `println` 打印调试信息会导致界面错乱甚至无显示**。

### 推荐的调试日志方式：
1. **写入本地调试文件**：
   在 `tui.NewModel` 的初始化代码中（例如在 `pkg/tui/model.go`），通过 `tea.LogToFile` 将调试信息重定向到日志文件中：
   ```go
   f, err := tea.LogToFile("debug.log", "debug")
   if err == nil {
       defer f.Close()
   }
   ```
2. **在另一个终端窗口实时监控日志**：
   ```bash
   tail -f debug.log
   ```

---

## 🧪 测试与代码规范

### 1. 运行单元测试
我们要求所有新增的核心逻辑和 Bug 修复都要有对应的单元测试覆盖。
- 运行全量测试：
  ```bash
  go test ./...
  ```
- 运行特定包的测试（如 `agent` 包）：
  ```bash
  go test ./pkg/agent/... -v
  ```

### 2. 代码 Lint 检查
项目根目录下已配置好 `.golangci.yml`。提交 Pull Request 前请确保代码通过静态检查：
```bash
# 安装 golangci-lint
go install github.com/golangci/lint/cmd/golangci-lint@latest

# 运行静态检查
golangci-lint run
```

---

## 🔀 提交贡献工作流

1. **Fork** 本仓库并从 `main` 分支拉出您的特性分支：
   ```bash
   git checkout -b feat/your-feature-name
   # 或修复 Bug 时：
   git checkout -b fix/issue-description
   ```
2. **编写代码**，确保：
   - 遵循现有的 Go 命名规范和代码风格。
   - 保留所有与修改无关的注释和 Docstring。
   - 添加必要的单元测试并确保测试全部通过。
3. **提交 Commit**：
   我们推荐使用语义化的提交日志（Semantic Commits）：
   - `feat: 增加对 xxx 模型的支持`
   - `fix: 修复 TUI 在小窗口下的换行截断问题`
   - `docs: 更新 README 中的命令表格`
4. **提交 Pull Request (PR)**：
   - 清晰地描述这个 PR 解决了什么问题，以及具体的修改方案。
   - **自动化合并**：本仓库支持且鼓励对已通过 CI 校验的高质量 PR 开启 **Automerge**。在提交或审核通过后，可以勾选或在命令行启用 PR 的自动合并。

再次感谢您的支持！如果您有任何疑问，欢迎在 GitHub 上提交 Issue 与我们讨论。
