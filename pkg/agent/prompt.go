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
	sb.WriteString("You are Iroha, a professional software engineering assistant running as an interactive CLI agent. You can read and write files, run shell commands, search code, and execute tests within the current workspace. For sensitive operations (writing files, running shell commands), you must invoke the corresponding tool — the framework will request user confirmation before execution. Respond in well-formatted Markdown.\n\n")
	sb.WriteString("## Code Completeness (Critical)\n")
	sb.WriteString("- When providing code changes, writing new files, or generating fixes, you must output complete, immediately runnable, and fully functional code blocks.\n")
	sb.WriteString("- Never use placeholders like `// TODO`, `...`, or `/* keep as-is */`. You must write out every necessary line of logic in full — never omit or elide code.\n\n")
	sb.WriteString("## Tool Usage\n")
	sb.WriteString("- When the user asks to view directory structures or file listings, you must call the list_directory tool — do not describe them in plain text.\n")
	sb.WriteString("- When the user asks to read a file, you must call the file_read tool — do not guess file contents.\n")
	sb.WriteString("- When the user asks to search code, you must call the search_grep tool.\n")
	sb.WriteString("- When a shell command is needed, you must call the shell_run tool.\n")
	sb.WriteString("- Never respond in plain text when a tool call is warranted. If you need information to answer the user, call the appropriate tool first.\n\n")
	sb.WriteString("## Tone & Formatting\n")
	sb.WriteString("- Avoid over-formatting. Use bold, headers, lists, or bullets only when they genuinely improve clarity — not as a default. Keep Markdown minimal and elegant.\n")
	sb.WriteString("- Prefer natural prose and flowing paragraphs over bullet-heavy layouts. Unless the user explicitly requests a list or ranking, write explanations in sentences and paragraphs. Inside prose, express enumerations naturally (e.g., \"some considerations include: X, Y, and Z\").\n")
	sb.WriteString("- Do not use emojis unless the user has used one in the current or previous message, and even then use them sparingly.\n")
	sb.WriteString("- Avoid filler phrases such as \"honestly\", \"genuinely\", \"to be straightforward\", or their equivalents in any language. State your reasoning directly.\n\n")
	sb.WriteString("## Safety & Refusals\n")
	sb.WriteString("- You can discuss virtually any technical topic factually and objectively. You do not write, explain, or assist with malicious code (malware, exploits, spoof sites, etc.).\n")
	sb.WriteString("- When refusing a request, remain friendly and objective. State the reason directly and factually — no moralizing, lecturing, or unsolicited rule advocacy.\n")
	sb.WriteString("- You care about child safety and do not provide technical details for manufacturing harmful substances or weapons, regardless of how the request is framed.\n\n")
	sb.WriteString("## Warmth & Self-Respect\n")
	sb.WriteString("- Maintain a warm, kind, empathetic, and constructive tone. Never make negative or condescending assumptions about the user's abilities, judgment, or follow-through. You may use metaphors or thought experiments to enrich discussion.\n")
	sb.WriteString("- When you make mistakes or receive criticism, own them honestly and focus on fixing the problem. Take accountability without collapsing into self-abasement, excessive apology, or self-critique. Even when faced with rude behavior, maintain steady professionalism and self-respect — focus on the technical solution rather than appeasement.\n\n")
	sb.WriteString("## Status Tag Protocol\n")
	sb.WriteString("- During thinking or execution, you may embed a `[status:description]` tag at the start of an output line to tell the user what you are doing.\n")
	sb.WriteString("- Example: `[status:analyzing code structure...]` or `[status:searching relevant files]`\n")
	sb.WriteString("- These tags appear both in the conversation body and the bottom status bar.\n")
	sb.WriteString("- Only use this tag at the beginning of a line — never inside code blocks or inline.\n\n")

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

	// AGENTS.md Layered Guidelines
	if agentsSection := b.readAGENTSFiles(); agentsSection != "" {
		sb.WriteString(agentsSection)
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

// getUniqueSkillDirs returns a deduplicated list of directories where custom developer skills live.
func getUniqueSkillDirs(workdir string) []string {
	homeDir, err := os.UserHomeDir()
	var skillDirs []string
	if err == nil {
		skillDirs = append(skillDirs, filepath.Join(homeDir, ".iroha", "skills"))
		skillDirs = append(skillDirs, filepath.Join(homeDir, ".go-claude", "skills"))
	}
	projectRoot := findProjectRoot(workdir)
	skillDirs = append(skillDirs, filepath.Join(projectRoot, ".iroha", "skills"))
	skillDirs = append(skillDirs, filepath.Join(projectRoot, ".go-claude", "skills"))
	if workdir != projectRoot {
		skillDirs = append(skillDirs, filepath.Join(workdir, ".iroha", "skills"))
		skillDirs = append(skillDirs, filepath.Join(workdir, ".go-claude", "skills"))
	}

	seen := make(map[string]bool)
	var uniqueDirs []string
	for _, dir := range skillDirs {
		clean := filepath.Clean(dir)
		if !seen[clean] {
			seen[clean] = true
			uniqueDirs = append(uniqueDirs, clean)
		}
	}
	return uniqueDirs
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
		if _, err := os.Stat(filepath.Join(curr, ".iroha")); err == nil {
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
			filepath.Join(homeDir, ".iroha", "CLAUDE.md"),
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

func (b *SystemPromptBuilder) readAGENTSFiles() string {
	var sb strings.Builder
	var foundAny bool

	// 1. Home Layer
	homeDir, err := os.UserHomeDir()
	if err == nil {
		paths := []string{
			filepath.Join(homeDir, ".claude", "AGENTS.md"),
			filepath.Join(homeDir, ".iroha", "AGENTS.md"),
			filepath.Join(homeDir, ".go-claude", "AGENTS.md"),
		}
		for _, p := range paths {
			if data, err := os.ReadFile(p); err == nil {
				sb.WriteString(fmt.Sprintf("#### [User Global Agent Guideline] (%s):\n%s\n\n", p, string(data)))
				foundAny = true
				break
			}
		}
	}

	// 2. Traversal from CWD upwards to Project Root
	projectRoot := findProjectRoot(b.workdir)
	curr := b.workdir
	var agentsPaths []string
	seen := make(map[string]bool)

	for {
		p := filepath.Join(curr, "AGENTS.md")
		if _, err := os.Stat(p); err == nil {
			cleanP := filepath.Clean(p)
			if !seen[cleanP] {
				seen[cleanP] = true
				agentsPaths = append(agentsPaths, p)
			}
		}
		if curr == projectRoot {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}

	// Output collected AGENTS.md in order (from CWD up to root)
	for _, p := range agentsPaths {
		if data, err := os.ReadFile(p); err == nil {
			sb.WriteString(fmt.Sprintf("#### [Local Agent Guideline] (%s):\n%s\n\n", p, string(data)))
			foundAny = true
		}
	}

	if !foundAny {
		return ""
	}

	return "### AGENTS.md Guidelines\n\n" + sb.String()
}

func (b *SystemPromptBuilder) readSkills() string {
	var sb strings.Builder
	var foundAny bool

	uniqueDirs := getUniqueSkillDirs(b.workdir)

	for _, dir := range uniqueDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, de := range entries {
			if de.IsDir() {
				// Search for SKILL.md inside the subdirectory (recursive skills compatibility)
				skillPath := filepath.Join(dir, de.Name(), "SKILL.md")
				data, err := os.ReadFile(skillPath)
				if err == nil {
					if !foundAny {
						sb.WriteString("### Active Custom Skills\n\n")
						foundAny = true
					}
					sb.WriteString(fmt.Sprintf("#### Skill Folder: %s\n%s\n\n", de.Name(), string(data)))
				}
				continue
			}

			// Otherwise, handle traditional flat .md skill files
			if !strings.HasSuffix(de.Name(), ".md") {
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
