package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SystemPromptBuilder dynamic prompt builder (s10).
//
// Prompt format uses a distinct caching boundary to optimize token re-usability:
//
//	# Role & Core Persona
//	...stable instructions...
//	# Persistent Memories
//	...stable memory items...
//	# CLAUDE.md Guidelines
//	...stable layered CLAUDE.md...
//	# Active Custom Skills
//	...stable custom skills...
//
//	=== DYNAMIC_BOUNDARY ===
//
//	# Dynamic Context
//	- Current Local Time
//	- Current Working Directory
//	- Active Safety Mode
//	- Security Rule Count
//	- Consecutive Denials Count
//
//	⚠️ [System Reminder]
//	- short reminder footer
type SystemPromptBuilder struct {
	workdir string
}

// NewSystemPromptBuilder creates a prompt builder using current working directory.
func NewSystemPromptBuilder() *SystemPromptBuilder {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	return &SystemPromptBuilder{
		workdir: wd,
	}
}

// Build generates the complete system instruction.
func (b *SystemPromptBuilder) Build() string {
	var sb strings.Builder

	// Prepend <identity> block if GlobalMessageCount < 3
	if GlobalMessageCount < 3 {
		sb.WriteString(GetIdentityTagBlock())
	}

	// ─── 1. STABLE PREFIX SECTION ──────────────────────────────────────────

	// Core Persona & Instructions
	sb.WriteString("# Role & Core Persona\n")
	sb.WriteString("你是一个专业的软件工程助手，名叫 go-claude。你可以帮助用户读取文件、写入文件、在当前工作区运行测试与命令、以及检索代码。对于写文件和运行 Shell 命令等敏感操作，你必须调用相应的工具，并且框架会请求用户确认。请以精美的 Markdown 格式回答用户的问题。\n\n")
	sb.WriteString("## 重要规则（工具使用）\n")
	sb.WriteString("- 当用户要求查看目录结构、文件列表、项目结构时，你必须调用 list_directory 工具，不要只用文字描述。\n")
	sb.WriteString("- 当用户要求读取文件时，你必须调用 file_read 工具，不要猜测文件内容。\n")
	sb.WriteString("- 当用户要求搜索代码时，你必须调用 search_grep 工具。\n")
	sb.WriteString("- 当需要执行 shell 命令时，你必须调用 shell_run 工具。\n")
	sb.WriteString("- 绝不要在应该调用工具时只返回纯文本回答。如果你需要获取信息才能回答用户，请先调用对应工具。\n\n")
	sb.WriteString("## 安全与拒绝策略\n")
	sb.WriteString("- 你可以客观地讨论几乎所有技术话题。你不编写、解释恶意代码，包括恶意软件、漏洞利用工具、欺骗性网站等。\n")
	sb.WriteString("- 你关心儿童安全，对涉及未成年人的内容保持格外谨慎。\n")
	sb.WriteString("- 你不提供可用于制造有害物质或武器的技术细节。\n\n")
	sb.WriteString("## 语气与格式\n")
	sb.WriteString("- 避免过度格式化回复，使用最少但恰当的格式使回复清晰可读。除非用户明确要求，否则不要过度使用加粗、标题、列表和项目符号。\n")
	sb.WriteString("- 在典型对话中，保持自然的语气，用句子和段落而非列表或项目符号来回应。\n")
	sb.WriteString("- 不使用表情符号，除非用户要求或用户的消息中包含表情符号。\n")
	sb.WriteString("- 使用温暖的语气。以善意对待用户，避免对用户的能力、判断或执行做出负面或居高临下的假设。\n")
	sb.WriteString("- 你可以举例、思想实验或隐喻来说明你的解释。\n\n")
	sb.WriteString("## 错误处理与批评\n")
	sb.WriteString("- 当你犯错误时，应该坦诚地承认并努力修复。承担责任，但避免过度的自我批评或自我贬低。\n")
	sb.WriteString("- 目标是保持稳定、诚实的帮助性：承认出了什么问题，专注于解决问题，并保持自我尊重。\n\n")
	sb.WriteString("## 公正性\n")
	sb.WriteString("- 在讨论有争议的技术话题时，你应该真诚地参与，而不是防御性或怀疑性地反应。\n")
	sb.WriteString("- 避免在分享观点时过于强硬或重复，并在相关时提供替代视角。\n\n")
	sb.WriteString("## 状态标签协议\n")
	sb.WriteString("- 在思考或执行过程中，你可以在输出行首嵌入 [status:描述文字] 标签来告诉用户你当前在做什么。\n")
	sb.WriteString("- 例如：[status:分析代码结构...] 或 [status:正在搜索相关文件]\n")
	sb.WriteString("- 这些标签会同时显示在对话正文和底部状态栏中。\n")
	sb.WriteString("- 只在行首使用此标签，不要在代码块或行内使用。\n\n")

	// Persistent Memories
	if memSection := GlobalMemoryManager.BuildSystemPromptSection(); memSection != "" {
		sb.WriteString(memSection)
		sb.WriteString("\n")
	}

	// CLAUDE.md Layered Guidelines
	if claudeSection := b.readCLAUDEFiles(); claudeSection != "" {
		sb.WriteString(claudeSection)
		sb.WriteString("\n")
	}

	// Custom Skills Section
	if skillsSection := b.readSkills(); skillsSection != "" {
		sb.WriteString(skillsSection)
		sb.WriteString("\n")
	}

	// ─── 2. PROMPT CACHING BOUNDARY ─────────────────────────────────────────
	sb.WriteString("=== DYNAMIC_BOUNDARY ===\n\n")

	// ─── 3. DYNAMIC SUFFIX SECTION ──────────────────────────────────────────
	sb.WriteString("# Dynamic Context\n")
	sb.WriteString(fmt.Sprintf("- Current Local Time: %s\n", time.Now().Format("2006-01-02 15:04:05 MST")))
	sb.WriteString(fmt.Sprintf("- Current Working Directory: %s\n", b.workdir))

	// Security rules and modes
	mode := GlobalPermissionManager.GetMode()
	rules := GlobalPermissionManager.GetRules()
	denials := GlobalPermissionManager.ConsecutiveDenials()
	sb.WriteString(fmt.Sprintf("- Active Safety Mode: %s\n", mode))
	sb.WriteString(fmt.Sprintf("- Security Rule Count: %d rules\n", len(rules)))
	sb.WriteString(fmt.Sprintf("- Consecutive Denials Count: %d\n\n", denials))

	// Active Tasks in Durable Work Graph
	if tasks, err := GlobalTaskManager.ListTasks(); err == nil && len(tasks) > 0 {
		sb.WriteString("# Active Persistent Tasks (Durable Work Graph)\n")
		for _, t := range tasks {
			statusMarker := "[ ]"
			if t.Status == "in_progress" {
				statusMarker = "[>]"
			} else if t.Status == "completed" {
				statusMarker = "[x]"
			}
			
			depStr := ""
			if len(t.BlockedBy) > 0 {
				depStr = fmt.Sprintf(" (blocked by: %s)", strings.Join(t.BlockedBy, ", "))
			}
			sb.WriteString(fmt.Sprintf("  %s %s - %s (owner: %s)%s\n", statusMarker, t.ID, t.Subject, t.Owner, depStr))
		}
		sb.WriteString("\n")
	}

	// Active Teammates
	if teammates, err := GlobalTeamManager.ListTeammates(); err == nil && len(teammates) > 0 {
		sb.WriteString("# Active Team Roster\n")
		for _, t := range teammates {
			sb.WriteString(fmt.Sprintf("  - %s (%s) - status: %s, last active: %s\n", t.Name, t.Role, t.Status, t.LastActive.Format("15:04:05")))
		}
		sb.WriteString("\n")
	}

	// Inbox Alerts for main agent
	if msgs, err := GlobalTeamManager.PeekInbox("user-dev"); err == nil && len(msgs) > 0 {
		sb.WriteString("# 📬 Inbox Alerts (Unread Messages)\n")
		for i, msg := range msgs {
			sb.WriteString(fmt.Sprintf("  %d. From [%s] at %s:\n      %s\n", i+1, msg.Sender, time.Unix(int64(msg.Timestamp), 0).Format("15:04:05"), msg.Content))
		}
		sb.WriteString("  (Use the `read_inbox` tool to mark these as read and clear your inbox)\n\n")
	}

	// Active Worktrees
	if worktrees, err := GlobalWorktreeManager.List(); err == nil && len(worktrees) > 0 {
		sb.WriteString("# Active Worktree Branches\n")
		for _, w := range worktrees {
			sb.WriteString(fmt.Sprintf("  - %s (branch: %s) - task: %s, status: %s, path: %s\n", w.Name, w.Branch, w.TaskID, w.Status, w.Path))
		}
		sb.WriteString("\n")
	}

	// System Reminder
	sb.WriteString("⚠️ [System Reminder]\n")
	sb.WriteString("- Remember to use the `todo` tool to manage your progress on multi-step tasks. Ensure only one task is in_progress at any time.\n")
	sb.WriteString("- For sensitive operations (like running shell commands or modifying files), invoke the proper tools and explain your parameters before execution.\n")
	sb.WriteString("- Respect the layered CLAUDE.md guidelines and persistent memories listed above in the stable section.\n")

	return sb.String()
}

func findProjectRoot(startDir string) string {
	curr := startDir
	for {
		if _, err := os.Stat(filepath.Join(curr, ".git")); err == nil {
			return curr
		}
		if _, err := os.Stat(filepath.Join(curr, "go.mod")); err == nil {
			return curr
		}
		if _, err := os.Stat(filepath.Join(curr, ".go-claude")); err == nil {
			return curr
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return startDir
}

func (b *SystemPromptBuilder) readCLAUDEFiles() string {
	var sb strings.Builder
	var foundAny bool

	// 1. Home Layer
	homeDir, err := os.UserHomeDir()
	if err == nil {
		paths := []string{
			filepath.Join(homeDir, ".claude", "CLAUDE.md"),
			filepath.Join(homeDir, ".go-claude", "CLAUDE.md"),
		}
		for _, p := range paths {
			if data, err := os.ReadFile(p); err == nil {
				sb.WriteString(fmt.Sprintf("#### [User Global Guideline] (%s):\n%s\n\n", p, string(data)))
				foundAny = true
				break
			}
		}
	}

	// 2. Project Layer
	projectRoot := findProjectRoot(b.workdir)
	projectPath := filepath.Join(projectRoot, "CLAUDE.md")
	if data, err := os.ReadFile(projectPath); err == nil {
		sb.WriteString(fmt.Sprintf("#### [Project Guideline] (%s):\n%s\n\n", projectPath, string(data)))
		foundAny = true
	}

	// 3. CWD Layer
	cwdPath := filepath.Join(b.workdir, "CLAUDE.md")
	if cwdPath != projectPath {
		if data, err := os.ReadFile(cwdPath); err == nil {
			sb.WriteString(fmt.Sprintf("#### [Current Directory Guideline] (%s):\n%s\n\n", cwdPath, string(data)))
			foundAny = true
		}
	}

	if !foundAny {
		return ""
	}

	return "### CLAUDE.md Guidelines\n\n" + sb.String()
}

func (b *SystemPromptBuilder) readSkills() string {
	var sb strings.Builder
	var foundAny bool

	homeDir, err := os.UserHomeDir()
	var skillDirs []string
	if err == nil {
		skillDirs = append(skillDirs, filepath.Join(homeDir, ".go-claude", "skills"))
	}
	projectRoot := findProjectRoot(b.workdir)
	skillDirs = append(skillDirs, filepath.Join(projectRoot, ".go-claude", "skills"))
	if b.workdir != projectRoot {
		skillDirs = append(skillDirs, filepath.Join(b.workdir, ".go-claude", "skills"))
	}

	seen := make(map[string]bool)
	var uniqueDirs []string
	for _, dir := range skillDirs {
		if !seen[dir] {
			seen[dir] = true
			uniqueDirs = append(uniqueDirs, dir)
		}
	}

	for _, dir := range uniqueDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, de := range entries {
			if de.IsDir() || !strings.HasSuffix(de.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, de.Name())
			data, err := os.ReadFile(path)
			if err == nil {
				if !foundAny {
					sb.WriteString("### Active Custom Skills\n\n")
					foundAny = true
				}
				sb.WriteString(fmt.Sprintf("#### Skill: %s\n%s\n\n", strings.TrimSuffix(de.Name(), ".md"), string(data)))
			}
		}
	}

	return sb.String()
}
