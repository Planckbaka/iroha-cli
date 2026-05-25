package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"iroha/pkg/config"
)

// WrapToolError enriches tool errors with actionable self-correction suggestions for the LLM
func WrapToolError(toolName string, args any, err error) error {
	if err == nil {
		return nil
	}

	errMsg := err.Error()

	// 1. Check for file not exist
	if errors.Is(err, os.ErrNotExist) || strings.Contains(errMsg, "no such file or directory") {
		return fmt.Errorf("%w\n[Self-repair suggestion] Please verify the file path is correct. If it may be a typo, use file_read/grep/search_grep to confirm the current directory structure or check if the target file exists.", err)
	}

	// 2. Check for permission issues
	if errors.Is(err, os.ErrPermission) || strings.Contains(errMsg, "permission denied") {
		return fmt.Errorf("%w\n[Self-repair suggestion] You do not have read/write permission for this path. Try writing to a different location within the workspace (current directory), or use shell_run to check permissions and directory attributes.", err)
	}

	// 3. Command execution failed
	if toolName == "shell_run" {
		return fmt.Errorf("%w\n[Self-repair suggestion] If the command failed due to a syntax error, incorrect arguments, or missing local dependencies, please verify your local development environment first. If a tool is missing, consider asking the user to authorize dependency installation, or use alternative Go commands to compile or test.", err)
	}

	return err
}

func getWorkdir(ctx context.Context) string {
	if ctx != nil {
		if val := ctx.Value(WorkdirKey); val != nil {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}

func resolvePath(ctx context.Context, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	workdir := getWorkdir(ctx)
	return filepath.Join(workdir, path)
}

// validateSandboxPath checks if the resolved absolute path resides under the current working directory.
func validateSandboxPath(ctx context.Context, rawPath string) error {
	cwd := getWorkdir(ctx)

	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return fmt.Errorf("invalid path format '%s': %w", rawPath, err)
	}

	cleanCWD := filepath.Clean(cwd)
	cleanAbs := filepath.Clean(absPath)

	if !strings.HasPrefix(cleanAbs, cleanCWD) {
		return fmt.Errorf("security sandbox blocked: path '%s' is outside the workspace root '%s'", rawPath, cleanCWD)
	}
	return nil
}

// checkShellCommandSandbox scans a shell command for relative path escaping or out-of-bounds absolute path accesses.
func checkShellCommandSandbox(ctx context.Context, command string) error {
	cwd := getWorkdir(ctx)
	cleanCWD := filepath.Clean(cwd)

	words := strings.Fields(command)
	for _, w := range words {
		// Clean quotes if any
		w = strings.Trim(w, "\"'`")

		// 1. Detect relative escaping
		if strings.Contains(w, "..") {
			abs, err := filepath.Abs(w)
			if err == nil {
				if !strings.HasPrefix(filepath.Clean(abs), cleanCWD) {
					return fmt.Errorf("security sandbox blocked: detected out-of-bounds relative path escape '%s' in command", w)
				}
			}
		}

		// 2. Detect absolute paths outside CWD
		if strings.HasPrefix(w, "/") {
			isSystemSafe := false
			safePrefixes := []string{"/bin", "/usr", "/opt", "/tmp", "/dev", "/etc/resolv.conf", "/etc/hosts", "/etc/ssl", "/var/run", "/private/tmp"}
			for _, prefix := range safePrefixes {
				if strings.HasPrefix(w, prefix) {
					isSystemSafe = true
					break
				}
			}
			if !isSystemSafe {
				cleanPath := filepath.Clean(w)
				if !strings.HasPrefix(cleanPath, cleanCWD) {
					return fmt.Errorf("security sandbox blocked: detected attempt to access absolute path '%s' outside the workspace in command", w)
				}
			}
		}
	}
	return nil
}

// GetSWETools returns the list of SWE tools plus our new todo tool for the Agent
func GetSWETools() ([]tool.Tool, error) {
	readTool, err := functiontool.New(functiontool.Config{
		Name:        "file_read",
		Description: "Read the contents of a file at the specified relative or absolute path.",
	}, FileReadHandler)
	if err != nil {
		return nil, err
	}

	writeTool, err := functiontool.New(functiontool.Config{
		Name:        "file_write",
		Description: "Write the specified content to a file. This overwrites the file if it already exists.",
	}, FileWriteHandler)
	if err != nil {
		return nil, err
	}

	editTool, err := functiontool.New(functiontool.Config{
		Name:        "file_edit",
		Description: "Edit a file by replacing exact text matches. Supports single and replace-all modes with optional dry-run preview.",
	}, FileEditHandler)
	if err != nil {
		return nil, err
	}

	grepTool, err := functiontool.New(functiontool.Config{
		Name:        "search_grep",
		Description: "Perform a global regex text search across the current directory, similar to grep/ripgrep.",
	}, GrepHandler)
	if err != nil {
		return nil, err
	}

	findTool, err := functiontool.New(functiontool.Config{
		Name:        "find_files",
		Description: "Find files matching a glob pattern. Supports ** for recursive matching. Excludes .git, node_modules, etc.",
	}, FindHandler)
	if err != nil {
		return nil, err
	}

	listDirTool, err := functiontool.New(functiontool.Config{
		Name:        "list_directory",
		Description: "List files and subdirectories under the specified directory. Supports recursive depth control and automatically skips large excluded directories like .git. This is the preferred tool for exploring project structure.",
	}, ListDirHandler)
	if err != nil {
		return nil, err
	}

	shellTool, err := functiontool.New(functiontool.Config{
		Name:        "shell_run",
		Description: "Execute a shell command. Only allowed within the current workspace directory.",
	}, ShellRunHandler)
	if err != nil {
		return nil, err
	}

	todoTool, err := functiontool.New(functiontool.Config{
		Name:        "todo",
		Description: "Rewrite or update the session-level plan list for the current multi-step task. Always call this tool first to create a plan for complex multi-step tasks, and update it when completing or starting steps. Exactly one task must be in the in_progress state at all times.",
	}, TodoHandler)
	if err != nil {
		return nil, err
	}

	// 6. memory_save — persist a durable fact across sessions
	memorySaveTool, err := functiontool.New(functiontool.Config{
		Name:        "memory_save",
		Description: "Save a persistent memory entry to disk that survives across sessions. Use this for user preferences, feedback corrections, project constraints, external resource pointers, or other critical information that cannot be re-derived from the codebase. Do not use for current task state, temporary branch names, secrets, or anything directly readable from the repository.",
	}, MemorySaveHandler)
	if err != nil {
		return nil, err
	}

	// 7. memory_list — read all currently loaded memories
	memoryListTool, err := functiontool.New(functiontool.Config{
		Name:        "memory_list",
		Description: "List all currently loaded persistent memory entries in the current session, grouped by type (user/feedback/project/reference).",
	}, MemoryListHandler)
	if err != nil {
		return nil, err
	}

	// 8. task_create
	taskCreateTool, err := functiontool.New(functiontool.Config{
		Name:        "task_create",
		Description: "Create a new task in the persistent task DAG. New tasks default to pending status with agent as the default owner.",
	}, TaskCreateHandler)
	if err != nil {
		return nil, err
	}

	// 9. task_update
	taskUpdateTool, err := functiontool.New(functiontool.Config{
		Name:        "task_update",
		Description: "Update an existing task in the persistent task DAG. Can modify status, upstream dependencies (blockedBy), or downstream dependencies (blocks). Any dependency cycles are rejected by DFS validation.",
	}, TaskUpdateHandler)
	if err != nil {
		return nil, err
	}

	// 10. task_list
	taskListTool, err := functiontool.New(functiontool.Config{
		Name:        "task_list",
		Description: "List all non-deleted tasks in the current persistent task DAG.",
	}, TaskListHandler)
	if err != nil {
		return nil, err
	}

	// 11. task_get
	taskGetTool, err := functiontool.New(functiontool.Config{
		Name:        "task_get",
		Description: "Get detailed information for a specific task by its ID, including upstream/downstream dependencies and execution status.",
	}, TaskGetHandler)
	if err != nil {
		return nil, err
	}

	// 12. background_run
	bgRunTool, err := functiontool.New(functiontool.Config{
		Name:        "background_run",
		Description: "Start a shell command in a background thread. Returns a task ID immediately without waiting for completion. Results are automatically fed back via the drain_notifications mechanism during the next interaction.",
	}, BackgroundRunHandler)
	if err != nil {
		return nil, err
	}

	// 13. check_background
	bgCheckTool, err := functiontool.New(functiontool.Config{
		Name:        "check_background",
		Description: "Query the status and abbreviated results of all or a specific background task. If no arguments are provided, lists all background tasks.",
	}, CheckBackgroundHandler)
	if err != nil {
		return nil, err
	}

	// 14. schedule_create
	schCreateTool, err := functiontool.New(functiontool.Config{
		Name:        "schedule_create",
		Description: "Create a new scheduled task (supports one-shot or recurring, with optional persistence). When the scheduled time arrives, the specified prompt is automatically fed to the LLM for execution.",
	}, ScheduleCreateHandler)
	if err != nil {
		return nil, err
	}

	// 15. schedule_list
	schListTool, err := functiontool.New(functiontool.Config{
		Name:        "schedule_list",
		Description: "List all currently active scheduled tasks (includes task ID, cron expression, recurring and durable properties).",
	}, ScheduleListHandler)
	if err != nil {
		return nil, err
	}

	// 16. schedule_delete
	schDeleteTool, err := functiontool.New(functiontool.Config{
		Name:        "schedule_delete",
		Description: "Delete an existing scheduled task by its task ID.",
	}, ScheduleDeleteHandler)
	if err != nil {
		return nil, err
	}

	// s15 Team Tools
	spawnTeammateTool, err := functiontool.New(functiontool.Config{
		Name:        "spawn_teammate",
		Description: "Spawn and start a teammate agent in the background.",
	}, SpawnTeammateHandler)
	if err != nil {
		return nil, err
	}

	listTeammatesTool, err := functiontool.New(functiontool.Config{
		Name:        "list_teammates",
		Description: "List the status and roles of all teammate agents in the current team.",
	}, ListTeammatesHandler)
	if err != nil {
		return nil, err
	}

	sendMessageTool, err := functiontool.New(functiontool.Config{
		Name:        "send_message",
		Description: "Send a message to a specific recipient's inbox.",
	}, SendMessageHandler)
	if err != nil {
		return nil, err
	}

	readInboxTool, err := functiontool.New(functiontool.Config{
		Name:        "read_inbox",
		Description: "Read and clear a teammate's inbox to pull new messages.",
	}, ReadInboxHandler)
	if err != nil {
		return nil, err
	}

	broadcastTool, err := functiontool.New(functiontool.Config{
		Name:        "broadcast",
		Description: "Broadcast a message to all team members.",
	}, BroadcastHandler)
	if err != nil {
		return nil, err
	}

	// s16 Protocol Tools
	protoShutdownReqTool, err := functiontool.New(functiontool.Config{
		Name:        "protocol_shutdown_request",
		Description: "Initiate a formal shutdown request.",
	}, ProtocolShutdownRequestHandler)
	if err != nil {
		return nil, err
	}

	protoShutdownRespTool, err := functiontool.New(functiontool.Config{
		Name:        "protocol_shutdown_response",
		Description: "Approve or reject a formal shutdown request.",
	}, ProtocolShutdownResponseHandler)
	if err != nil {
		return nil, err
	}

	protoPlanApprovalReqTool, err := functiontool.New(functiontool.Config{
		Name:        "protocol_plan_approval_request",
		Description: "Submit a major action plan or refactoring proposal for approval.",
	}, ProtocolPlanApprovalRequestHandler)
	if err != nil {
		return nil, err
	}

	protoPlanApprovalRespTool, err := functiontool.New(functiontool.Config{
		Name:        "protocol_plan_approval_response",
		Description: "Approve or reject an action plan proposal.",
	}, ProtocolPlanApprovalResponseHandler)
	if err != nil {
		return nil, err
	}

	// s17 Autonomous Agent Tools
	agentClaimTaskTool, err := functiontool.New(functiontool.Config{
		Name:        "agent_claim_task",
		Description: "Allow a teammate agent to claim all pending and unblocked tasks matching the given keywords.",
	}, AgentClaimTaskHandler)
	if err != nil {
		return nil, err
	}

	agentSetStateTool, err := functiontool.New(functiontool.Config{
		Name:        "agent_set_state",
		Description: "Set the agent's state. Options: WORK (focused work mode), IDLE (idle polling mode).",
	}, AgentSetStateHandler)
	if err != nil {
		return nil, err
	}

	// s18 Worktree Tools
	wtCreateTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_create",
		Description: "Create an isolated Git worktree branch for dedicated and safe development of a specific task.",
	}, WorktreeCreateHandler)
	if err != nil {
		return nil, err
	}

	wtListTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_list",
		Description: "List all current workspace isolation branches and directories.",
	}, WorktreeListHandler)
	if err != nil {
		return nil, err
	}

	wtStatusTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_status",
		Description: "Query the detailed status of a specific isolated workspace.",
	}, WorktreeStatusHandler)
	if err != nil {
		return nil, err
	}

	wtEnterTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_enter",
		Description: "Log entry into or activation of an isolated workspace.",
	}, WorktreeEnterHandler)
	if err != nil {
		return nil, err
	}

	wtCloseoutTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_closeout",
		Description: "Close out a specific isolated workspace branch. Choose to keep the path (keep) or forcefully remove it (remove).",
	}, WorktreeCloseoutHandler)
	if err != nil {
		return nil, err
	}

	// s19 MCP Tool
	mcpServerListTool, err := functiontool.New(functiontool.Config{
		Name:        "mcp_server_list",
		Description: "List all currently connected external MCP plugin servers and their connection status.",
	}, MCPServerListHandler)
	if err != nil {
		return nil, err
	}

	// s20 CI Watcher Tool
	ciWatchTool, err := functiontool.New(functiontool.Config{
		Name:        "agent_watch_ci",
		Description: "Start a background process to monitor GitHub Actions CI status and send inbox notifications on failures.",
	}, AgentWatchCIHandler)
	if err != nil {
		return nil, err
	}

	// s21 LSP Tools — load user LSP server config if available
	if cfg, err := config.LoadConfig(); err == nil && len(cfg.LSPServers) > 0 {
		servers := make([]LSPServerConfig, len(cfg.LSPServers))
		for i, s := range cfg.LSPServers {
			servers[i] = LSPServerConfig{
				Language:     s.Language,
				Command:      s.Command,
				Args:         s.Args,
				FilePatterns: s.FilePatterns,
			}
		}
		SetLSPServers(servers)
	}

	lspGotoDefinitionTool, err := functiontool.New(functiontool.Config{
		Name:        "lsp_goto_definition",
		Description: "Locate the declaration and definition of a symbol at a specific line and column position via LSP. Supports Go, TypeScript, Python, Rust, and other configured language servers. Returns the defining file path, line number, and code snippet preview.",
	}, LSPGotoDefinitionHandler)
	if err != nil {
		return nil, err
	}

	lspFindReferencesTool, err := functiontool.New(functiontool.Config{
		Name:        "lsp_find_references",
		Description: "Find all references and usages of a symbol at a specific position across the workspace via LSP. Supports Go, TypeScript, Python, Rust, and other configured language servers.",
	}, LSPFindReferencesHandler)
	if err != nil {
		return nil, err
	}

	lspDocumentSymbolsTool, err := functiontool.New(functiontool.Config{
		Name:        "lsp_document_symbols",
		Description: "Extract and list all semantic symbols (classes, structs, methods, functions, variables, etc.) from a specified file via LSP. Supports Go, TypeScript, Python, Rust, and other configured language servers.",
	}, LSPDocumentSymbolsHandler)
	if err != nil {
		return nil, err
	}

	resTools := []tool.Tool{
		readTool, writeTool, editTool, listDirTool, grepTool, findTool, shellTool, todoTool,
		memorySaveTool, memoryListTool,
		taskCreateTool, taskUpdateTool, taskListTool, taskGetTool,
		bgRunTool, bgCheckTool,
		schCreateTool, schListTool, schDeleteTool,

		// s15
		spawnTeammateTool, listTeammatesTool, sendMessageTool, readInboxTool, broadcastTool,
		// s16
		protoShutdownReqTool, protoShutdownRespTool, protoPlanApprovalReqTool, protoPlanApprovalRespTool,
		// s17
		agentClaimTaskTool, agentSetStateTool,
		// s18
		wtCreateTool, wtListTool, wtStatusTool, wtEnterTool, wtCloseoutTool,
		// s19
		mcpServerListTool,
		// s20
		ciWatchTool,
		// s21
		lspGotoDefinitionTool, lspFindReferencesTool, lspDocumentSymbolsTool,
	}

	// s19 Dynamic MCP Tools
	_ = GlobalMCPRouter.LoadAndStartPlugins()
	if mcpTools, err := GlobalMCPRouter.DiscoverTools(); err == nil {
		resTools = append(resTools, mcpTools...)
	}

	return resTools, nil
}
