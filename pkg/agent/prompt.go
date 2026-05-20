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
	sb.WriteString("你是一个专业的软件工程助手，名叫 go-claude。你可以帮助用户读取文件、写入文件、在当前工作区运行测试与命令、以及检索代码。对于写文件 and 运行 Shell 命令等敏感操作，你必须调用相应的工具，并且框架会请求用户确认。请以精美的 Markdown 格式回答用户的问题。\n\n")

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
