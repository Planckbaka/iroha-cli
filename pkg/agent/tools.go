package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// WrapToolError enriches tool errors with actionable self-correction suggestions for the LLM
func WrapToolError(toolName string, args any, err error) error {
	if err == nil {
		return nil
	}

	errMsg := err.Error()

	// 1. Check for file not exist
	if errors.Is(err, os.ErrNotExist) || strings.Contains(errMsg, "no such file or directory") {
		return fmt.Errorf("%w\n【自我修复建议】请检查文件路径是否正确。如果是拼写错误，请使用 file_read/grep/search_grep 重新确认当前目录结构，或确认目标文件是否存在。", err)
	}

	// 2. Check for permission issues
	if errors.Is(err, os.ErrPermission) || strings.Contains(errMsg, "permission denied") {
		return fmt.Errorf("%w\n【自我修复建议】您似乎没有该路径的读写权限。请尝试写入至工作区（当前目录）下的其他位置，或使用 shell_run 检查权限及目录属性。", err)
	}

	// 3. Command execution failed
	if toolName == "shell_run" {
		return fmt.Errorf("%w\n【自我修复建议】如果该命令失败是因为语法错误、参数错误或缺少本地依赖依赖，请先确认本地开发环境。如果缺少某个工具，可以尝试让用户授权安装依赖，或换用其他 Go 命令完成编译或测试工作。", err)
	}

	return err
}

// validateSandboxPath checks if the resolved absolute path resides under the current working directory.
func validateSandboxPath(rawPath string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("无法获取当前工作目录: %w", err)
	}

	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return fmt.Errorf("无效的路径格式 '%s': %w", rawPath, err)
	}

	cleanCWD := filepath.Clean(cwd)
	cleanAbs := filepath.Clean(absPath)

	if !strings.HasPrefix(cleanAbs, cleanCWD) {
		return fmt.Errorf("⚠️ 安全沙箱阻断: 路径 '%s' 超出了工作区根目录范围 '%s'", rawPath, cleanCWD)
	}
	return nil
}

// checkShellCommandSandbox scans a shell command for relative path escaping or out-of-bounds absolute path accesses.
func checkShellCommandSandbox(command string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
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
					return fmt.Errorf("⚠️ 安全沙箱阻断: 检测到命令中包含越界的相对路径逃逸 '%s'", w)
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
					return fmt.Errorf("⚠️ 安全沙箱阻断: 检测到命令试图访问工作区外的绝对路径 '%s'", w)
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
		Description: "读取指定相对或绝对路径的文件内容。",
	}, FileReadHandler)
	if err != nil {
		return nil, err
	}

	writeTool, err := functiontool.New(functiontool.Config{
		Name:        "file_write",
		Description: "向文件写入指定内容。这会覆盖原文件（如果有的话）。",
	}, FileWriteHandler)
	if err != nil {
		return nil, err
	}

	grepTool, err := functiontool.New(functiontool.Config{
		Name:        "search_grep",
		Description: "对当前目录进行正则表达式全局文本搜索，类似于 grep/ripgrep。",
	}, GrepHandler)
	if err != nil {
		return nil, err
	}

	listDirTool, err := functiontool.New(functiontool.Config{
		Name:        "list_directory",
		Description: "列出指定目录下的文件和子目录。支持递归深度控制，自动跳过 .git 等大型排除目录。这是查看项目结构的首选工具。",
	}, ListDirHandler)
	if err != nil {
		return nil, err
	}

	shellTool, err := functiontool.New(functiontool.Config{
		Name:        "shell_run",
		Description: "执行一条 Shell 命令。只允许在当前工作区目录下执行。",
	}, ShellRunHandler)
	if err != nil {
		return nil, err
	}

	todoTool, err := functiontool.New(functiontool.Config{
		Name:        "todo",
		Description: "重写或更新当前多步骤工作的会话级规划清单。每次多步骤复杂任务都应该首先调用此工具制定计划，并且在完成某些步骤或进入新步骤时进行更新。强制要求有且仅能有一个任务处于 in_progress 状态。",
	}, TodoHandler)
	if err != nil {
		return nil, err
	}

	// 6. memory_save — persist a durable fact across sessions
	memorySaveTool, err := functiontool.New(functiontool.Config{
		Name:        "memory_save",
		Description: "将一条跨会话的持久化记忆条目保存到磁盘。适用于用户偏好、反馈更正、项目约束、外部资源指针等不易从代码库中重新推导出的关键信息。不要用于存储当前任务状态、临时分支名、密钥或任何可从仓库中直接读取的内容。",
	}, MemorySaveHandler)
	if err != nil {
		return nil, err
	}

	// 7. memory_list — read all currently loaded memories
	memoryListTool, err := functiontool.New(functiontool.Config{
		Name:        "memory_list",
		Description: "列出当前会话中已加载的所有持久化记忆条目，按类型分组显示（user/feedback/project/reference）。",
	}, MemoryListHandler)
	if err != nil {
		return nil, err
	}

	// 8. task_create
	taskCreateTool, err := functiontool.New(functiontool.Config{
		Name:        "task_create",
		Description: "在持久化任务 DAG 图中创建一个新任务。新任务默认状态为 pending，默认负责人为 agent。",
	}, TaskCreateHandler)
	if err != nil {
		return nil, err
	}

	// 9. task_update
	taskUpdateTool, err := functiontool.New(functiontool.Config{
		Name:        "task_update",
		Description: "更新持久化任务 DAG 图中的现有任务。可以修改状态、前置依赖或后续依赖。任何依赖环（cycle）都会被 DFS 校验直接拒绝。",
	}, TaskUpdateHandler)
	if err != nil {
		return nil, err
	}

	// 10. task_list
	taskListTool, err := functiontool.New(functiontool.Config{
		Name:        "task_list",
		Description: "列出当前持久化任务 DAG 图中所有未删除的任务列表。",
	}, TaskListHandler)
	if err != nil {
		return nil, err
	}

	// 11. task_get
	taskGetTool, err := functiontool.New(functiontool.Config{
		Name:        "task_get",
		Description: "根据任务 ID 获取特定任务的详细记录，包含前置/后续依赖与执行状态。",
	}, TaskGetHandler)
	if err != nil {
		return nil, err
	}

	// 12. background_run
	bgRunTool, err := functiontool.New(functiontool.Config{
		Name:        "background_run",
		Description: "在后台子线程启动一条 Shell 命令执行。会立即返回任务 ID，大模型不需要在此等待。完成后结果将自动通过 drain_notifications 机制在下次交互时反馈给你。",
	}, BackgroundRunHandler)
	if err != nil {
		return nil, err
	}

	// 13. check_background
	bgCheckTool, err := functiontool.New(functiontool.Config{
		Name:        "check_background",
		Description: "查询所有或特定后台任务的状态与缩略结果。若不传参数则列出全部后台任务的列表。",
	}, CheckBackgroundHandler)
	if err != nil {
		return nil, err
	}

	// 14. schedule_create
	schCreateTool, err := functiontool.New(functiontool.Config{
		Name:        "schedule_create",
		Description: "创建一个新的定时调度任务（支持单次或循环定时，支持持久化）。当时间到达时，其指定的提示指令会自动被反馈给大模型执行。",
	}, ScheduleCreateHandler)
	if err != nil {
		return nil, err
	}

	// 15. schedule_list
	schListTool, err := functiontool.New(functiontool.Config{
		Name:        "schedule_list",
		Description: "列出当前所有活跃的定时调度任务（包含任务 ID、Cron 表达式、循环与持久化属性）。",
	}, ScheduleListHandler)
	if err != nil {
		return nil, err
	}

	// 16. schedule_delete
	schDeleteTool, err := functiontool.New(functiontool.Config{
		Name:        "schedule_delete",
		Description: "根据任务 ID 删除一个现有的定时调度任务。",
	}, ScheduleDeleteHandler)
	if err != nil {
		return nil, err
	}

	// s15 Team Tools
	spawnTeammateTool, err := functiontool.New(functiontool.Config{
		Name:        "spawn_teammate",
		Description: "生成并在后台启动一个特工代理人角色。",
	}, SpawnTeammateHandler)
	if err != nil {
		return nil, err
	}

	listTeammatesTool, err := functiontool.New(functiontool.Config{
		Name:        "list_teammates",
		Description: "列出当前团队中所有特工代理人的状态与角色。",
	}, ListTeammatesHandler)
	if err != nil {
		return nil, err
	}

	sendMessageTool, err := functiontool.New(functiontool.Config{
		Name:        "send_message",
		Description: "向指定接收者的信箱发送一条消息。",
	}, SendMessageHandler)
	if err != nil {
		return nil, err
	}

	readInboxTool, err := functiontool.New(functiontool.Config{
		Name:        "read_inbox",
		Description: "读取并清空某个特工的信箱，以拉取新消息。",
	}, ReadInboxHandler)
	if err != nil {
		return nil, err
	}

	broadcastTool, err := functiontool.New(functiontool.Config{
		Name:        "broadcast",
		Description: "广播消息给所有的团队成员。",
	}, BroadcastHandler)
	if err != nil {
		return nil, err
	}

	// s16 Protocol Tools
	protoShutdownReqTool, err := functiontool.New(functiontool.Config{
		Name:        "protocol_shutdown_request",
		Description: "发起一个正式的停机申请请求。",
	}, ProtocolShutdownRequestHandler)
	if err != nil {
		return nil, err
	}

	protoShutdownRespTool, err := functiontool.New(functiontool.Config{
		Name:        "protocol_shutdown_response",
		Description: "对正式的停机申请请求做出回应批准或拒绝。",
	}, ProtocolShutdownResponseHandler)
	if err != nil {
		return nil, err
	}

	protoPlanApprovalReqTool, err := functiontool.New(functiontool.Config{
		Name:        "protocol_plan_approval_request",
		Description: "发起一个重大的行动方案或重构计划审批请求。",
	}, ProtocolPlanApprovalRequestHandler)
	if err != nil {
		return nil, err
	}

	protoPlanApprovalRespTool, err := functiontool.New(functiontool.Config{
		Name:        "protocol_plan_approval_response",
		Description: "审批或拒绝其他的行动方案请求。",
	}, ProtocolPlanApprovalResponseHandler)
	if err != nil {
		return nil, err
	}

	// s17 Autonomous Agent Tools
	agentClaimTaskTool, err := functiontool.New(functiontool.Config{
		Name:        "agent_claim_task",
		Description: "让某个特工代理人根据关键字过滤匹配并认领所有 pending 且 unblocked 的任务。",
	}, AgentClaimTaskHandler)
	if err != nil {
		return nil, err
	}

	agentSetStateTool, err := functiontool.New(functiontool.Config{
		Name:        "agent_set_state",
		Description: "修改特工的状态，可选：WORK（专注工作模式）、IDLE（闲置轮询模式）。",
	}, AgentSetStateHandler)
	if err != nil {
		return nil, err
	}

	// s18 Worktree Tools
	wtCreateTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_create",
		Description: "创建一个隔离的 Git worktree 分支，用于专职且安全地开发某个特定任务。",
	}, WorktreeCreateHandler)
	if err != nil {
		return nil, err
	}

	wtListTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_list",
		Description: "列出当前所有的工作区隔离分支和目录。",
	}, WorktreeListHandler)
	if err != nil {
		return nil, err
	}

	wtStatusTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_status",
		Description: "查询特定隔离工作区的具体状态详情。",
	}, WorktreeStatusHandler)
	if err != nil {
		return nil, err
	}

	wtEnterTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_enter",
		Description: "记录进入或激活某个隔离工作区的访问记录。",
	}, WorktreeEnterHandler)
	if err != nil {
		return nil, err
	}

	wtCloseoutTool, err := functiontool.New(functiontool.Config{
		Name:        "worktree_closeout",
		Description: "收尾某个特定的隔离工作区分支，可以选择保留路径(keep)或强力移除(remove)。",
	}, WorktreeCloseoutHandler)
	if err != nil {
		return nil, err
	}

	// s19 MCP Tool
	mcpServerListTool, err := functiontool.New(functiontool.Config{
		Name:        "mcp_server_list",
		Description: "列出当前全部已连接 of 外部 MCP 插件服务器及其连接状态。",
	}, MCPServerListHandler)
	if err != nil {
		return nil, err
	}

	// s20 CI Watcher Tool
	ciWatchTool, err := functiontool.New(functiontool.Config{
		Name:        "agent_watch_ci",
		Description: "启动后台进程监听 GitHub Actions CI 状态，报错时发送收件箱通知。",
	}, AgentWatchCIHandler)
	if err != nil {
		return nil, err
	}

	resTools := []tool.Tool{
		readTool, writeTool, listDirTool, grepTool, shellTool, todoTool,
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
	}

	// s19 Dynamic MCP Tools
	_ = GlobalMCPRouter.LoadAndStartPlugins()
	if mcpTools, err := GlobalMCPRouter.DiscoverTools(); err == nil {
		resTools = append(resTools, mcpTools...)
	}

	return resTools, nil
}
