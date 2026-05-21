package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

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

// 1. file_read
type FileReadArgs struct {
	Path string `json:"path" description:"要读取的文件路径（相对或绝对路径）"`
}

type FileReadResult struct {
	Content string `json:"content" description:"文件内容"`
}

const maxFileReadSize = 10 * 1024 * 1024 // 10MB

func FileReadHandler(ctx tool.Context, args FileReadArgs) (FileReadResult, error) {
	info, err := os.Stat(args.Path)
	if err != nil {
		return FileReadResult{}, WrapToolError("file_read", args, fmt.Errorf("读取文件失败: %w", err))
	}
	if info.IsDir() {
		return FileReadResult{}, fmt.Errorf("'%s' 是一个目录，不是文件。请使用 shell_run 执行 ls 或 find 命令来查看目录结构", args.Path)
	}
	if info.Size() > maxFileReadSize {
		return FileReadResult{}, fmt.Errorf("文件 '%s' 大小为 %d 字节，超过 10MB 读取限制。请使用 shell_run 配合 head/tail 来分段读取", args.Path, info.Size())
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return FileReadResult{}, WrapToolError("file_read", args, fmt.Errorf("读取文件失败: %w", err))
	}
	return FileReadResult{Content: string(data)}, nil
}

// 2. file_write (需要人机确认)
type FileWriteArgs struct {
	Path    string `json:"path" description:"要写入的文件路径"`
	Content string `json:"content" description:"要写入的文本内容"`
}

type FileWriteResult struct {
	Success bool `json:"success" description:"是否写入成功"`
}

func FileWriteHandler(ctx tool.Context, args FileWriteArgs) (FileWriteResult, error) {
	// Create parent directories if they don't exist
	dir := filepath.Dir(args.Path)
	if dir != "." && dir != "/" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return FileWriteResult{Success: false}, WrapToolError("file_write", args, fmt.Errorf("创建父目录失败: %w", err))
		}
	}

	err := os.WriteFile(args.Path, []byte(args.Content), 0644)
	if err != nil {
		return FileWriteResult{Success: false}, WrapToolError("file_write", args, fmt.Errorf("写入文件失败: %w", err))
	}
	return FileWriteResult{Success: true}, nil
}

// 3. list_directory
type ListDirArgs struct {
	Path     string `json:"path" description:"要列出的目录路径（默认为当前工作目录）"`
	MaxDepth int    `json:"max_depth,omitempty" description:"递归深度（默认 1，仅当前层级；最大 4）"`
}

type ListDirResult struct {
	Entries []string `json:"entries" description:"目录条目列表（带 / 后缀表示子目录）"`
}

func ListDirHandler(ctx tool.Context, args ListDirArgs) (ListDirResult, error) {
	if args.Path == "" {
		args.Path = "."
	}
	if args.MaxDepth <= 0 {
		args.MaxDepth = 1
	}
	if args.MaxDepth > 4 {
		args.MaxDepth = 4
	}

	cwd, _ := os.Getwd()
	root := args.Path
	if !filepath.IsAbs(root) {
		root = filepath.Join(cwd, root)
	}

	var entries []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip excluded directories
		if info.IsDir() && grepExcludedDirs[info.Name()] {
			return filepath.SkipDir
		}

		// Calculate current depth
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if rel == "." {
			return nil // skip root itself
		}

		depth := len(strings.Split(rel, string(filepath.Separator)))
		if depth > args.MaxDepth {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			entries = append(entries, rel+"/")
		} else {
			entries = append(entries, rel)
		}

		if len(entries) >= 200 {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return ListDirResult{}, WrapToolError("list_directory", args, err)
	}

	return ListDirResult{Entries: entries}, nil
}

// 4. search_grep
type GrepArgs struct {
	Pattern string `json:"pattern" description:"正则表达式搜索模式"`
}

type GrepResult struct {
	Matches []string `json:"matches" description:"匹配到的行列表"`
}

var grepExcludedDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true,
	"vendor": true, "__pycache__": true, ".next": true,
	"dist": true, "build": true, ".cache": true,
}

const maxGrepFileSize = 1 * 1024 * 1024 // 1MB

func GrepHandler(ctx tool.Context, args GrepArgs) (GrepResult, error) {
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return GrepResult{}, fmt.Errorf("无效的正则表达式: %w", err)
	}

	var matches []string
	cwd, _ := os.Getwd()

	err = filepath.Walk(cwd, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip excluded directories entirely — must return SkipDir, not nil
		if info.IsDir() {
			if grepExcludedDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip large files
		if info.Size() > maxGrepFileSize {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(cwd, path)
		lines := bytes.Split(data, []byte("\n"))
		for i, line := range lines {
			if re.Match(line) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, i+1, string(line)))
				if len(matches) >= 50 {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	return GrepResult{Matches: matches}, err
}

// 4. shell_run (需要极其严格的人机确认)
type ShellRunArgs struct {
	Command string `json:"command" description:"要执行的本地 Shell 命令"`
}

type ShellRunResult struct {
	Output   string `json:"output" description:"命令的标准输出和标准错误输出合并内容"`
	ExitCode int    `json:"exit_code" description:"退出状态码"`
}

const shellRunTimeout = 30 * time.Second

const maxStreamLines = 500

func ShellRunHandler(ctx tool.Context, args ShellRunArgs) (ShellRunResult, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), shellRunTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", args.Command)

	var outBuf bytes.Buffer
	pr, pw := io.Pipe()
	multiWriter := io.MultiWriter(&outBuf, pw)

	cmd.Stdout = multiWriter
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return ShellRunResult{}, err
	}
	defer func() { _ = cmd.Process.Kill() }()

	// stderr 合并 goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(multiWriter, stderr)
	}()

	// 逐行流式扫描
	scanner := bufio.NewScanner(pr)
	lineCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		lineCount++
		if lineCount <= maxStreamLines {
			ToolBridge.Send(ToolStatus{
				Name:        "shell_run",
				Running:     true,
				StreamLines: []string{line},
			})
		}
	}

	// 顺序保证：scanner EOF → join stderr goroutine → 关闭 pipe writer → cmd.Wait()
	wg.Wait()
	_ = pw.Close()
	_ = cmd.Wait()

	// 构建最终结果
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	outputStr := outBuf.String()
	if runCtx.Err() == context.DeadlineExceeded {
		outputStr += "\n[超时] 命令执行超过 30 秒，已被强制终止。"
		exitCode = -1
	}

	if exitCode != 0 {
		wrappedErr := WrapToolError("shell_run", args, fmt.Errorf("命令运行失败 (exit code %d)", exitCode))
		outputStr += "\n" + wrappedErr.Error()
	}

	return ShellRunResult{
		Output:   outputStr,
		ExitCode: exitCode,
	}, nil
}

// 5. todo (会话进度规划工具)
type TodoArgs struct {
	Items []TodoItem `json:"items" description:"要更新的整个规划列表项"`
}

type TodoResult struct {
	RenderedPlan string `json:"rendered_plan" description:"格式化渲染后的当前进度表"`
}

func TodoHandler(ctx tool.Context, args TodoArgs) (TodoResult, error) {
	err := GlobalTodoManager.Update(args.Items)
	if err != nil {
		return TodoResult{}, fmt.Errorf("更新任务清单失败: %w", err)
	}
	return TodoResult{RenderedPlan: GlobalTodoManager.Render()}, nil
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
		Name:                "file_write",
		Description:         "向文件写入指定内容。这会覆盖原文件（如果有的话）。",
		RequireConfirmation: true, // 静态标记，在执行 Run 前强制人机确认
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
		Name:                "shell_run",
		Description:         "执行一条 Shell 命令。只允许在当前工作区目录下执行。",
		RequireConfirmation: true, // 静态标记，在执行 Run 前极其需要人机确认
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

// ─── Memory tool handlers ─────────────────────────────────────────────────

// MemorySaveArgs mirrors the four fields a memory entry needs.
type MemorySaveArgs struct {
	Name        string `json:"name"        description:"记忆条目的唯一标识名称（英文、下划线分隔）"`
	Description string `json:"description" description:"一行简短描述，用于索引和系统提示中展示"`
	Type        string `json:"type"        description:"记忆类型：user（用户偏好）、feedback（反馈更正）、project（项目事实）、reference（外部资源指针）"`
	Content     string `json:"content"     description:"记忆的详细正文内容"`
}

type MemorySaveResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func MemorySaveHandler(_ tool.Context, args MemorySaveArgs) (MemorySaveResult, error) {
	err := GlobalMemoryManager.Save(args.Name, args.Description, MemoryType(args.Type), args.Content)
	if err != nil {
		return MemorySaveResult{OK: false, Message: err.Error()}, nil
	}
	return MemorySaveResult{OK: true, Message: "✅ 记忆已保存: " + args.Name}, nil
}

// MemoryListArgs — no parameters needed, lists everything.
type MemoryListArgs struct{}

type MemoryListResult struct {
	Total   int                        `json:"total"`
	Entries map[string][]MemoryListRow `json:"entries"`
}

type MemoryListRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func MemoryListHandler(_ tool.Context, _ MemoryListArgs) (MemoryListResult, error) {
	all := GlobalMemoryManager.List()
	out := MemoryListResult{
		Entries: make(map[string][]MemoryListRow),
	}
	for t, entries := range all {
		for _, e := range entries {
			out.Entries[string(t)] = append(out.Entries[string(t)], MemoryListRow{
				Name:        e.Name,
				Description: e.Description,
			})
			out.Total++
		}
	}
	return out, nil
}

// ─── Task tool definitions ──────────────────────────────────────────────────

// TaskCreateArgs represents arguments for task_create tool.
type TaskCreateArgs struct {
	ID          string   `json:"id" description:"任务唯一标识，如 t1, task-setup"`
	Subject     string   `json:"subject" description:"任务主题/简短摘要"`
	Description string   `json:"description,omitempty" description:"任务详细描述"`
	Status      string   `json:"status,omitempty" description:"任务状态，默认为 pending，可选：pending, in_progress, completed"`
	BlockedBy   []string `json:"blockedBy,omitempty" description:"依赖的前置任务 ID 列表"`
	Blocks      []string `json:"blocks,omitempty" description:"该任务阻塞的后续任务 ID 列表"`
	Owner       string   `json:"owner,omitempty" description:"任务负责人，默认为 agent，可选：agent, user"`
}

type TaskCreateResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func TaskCreateHandler(ctx tool.Context, args TaskCreateArgs) (TaskCreateResult, error) {
	status := args.Status
	if status == "" {
		status = "pending"
	}
	owner := args.Owner
	if owner == "" {
		owner = "agent"
	}
	task := &TaskRecord{
		ID:          args.ID,
		Subject:     args.Subject,
		Description: args.Description,
		Status:      status,
		BlockedBy:   args.BlockedBy,
		Blocks:      args.Blocks,
		Owner:       owner,
	}
	if err := GlobalTaskManager.SaveTask(task); err != nil {
		return TaskCreateResult{Success: false, Message: err.Error()}, WrapToolError("task_create", args, err)
	}
	return TaskCreateResult{Success: true, Message: fmt.Sprintf("✅ 任务已创建: %s", task.ID)}, nil
}

// TaskUpdateArgs represents arguments for task_update tool.
type TaskUpdateArgs struct {
	ID          string   `json:"id" description:"要更新的任务唯一标识"`
	Subject     string   `json:"subject,omitempty" description:"新的任务主题"`
	Description string   `json:"description,omitempty" description:"新的任务描述"`
	Status      string   `json:"status,omitempty" description:"新的任务状态，可选：pending, in_progress, completed, deleted"`
	BlockedBy   []string `json:"blockedBy,omitempty" description:"新的前置依赖任务 ID 列表（若传入，则完全覆盖原有列表）"`
	Blocks      []string `json:"blocks,omitempty" description:"新的后续依赖任务 ID 列表（若传入，则完全覆盖原有列表）"`
	Owner       string   `json:"owner,omitempty" description:"新的负责人"`
}

type TaskUpdateResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func TaskUpdateHandler(ctx tool.Context, args TaskUpdateArgs) (TaskUpdateResult, error) {
	existing, err := GlobalTaskManager.GetTask(args.ID)
	if err != nil {
		return TaskUpdateResult{Success: false, Message: fmt.Sprintf("任务未找到: %s", args.ID)}, WrapToolError("task_update", args, err)
	}

	if args.Subject != "" {
		existing.Subject = args.Subject
	}
	if args.Description != "" {
		existing.Description = args.Description
	}
	if args.Status != "" {
		existing.Status = args.Status
	}
	if args.BlockedBy != nil {
		existing.BlockedBy = args.BlockedBy
	}
	if args.Blocks != nil {
		existing.Blocks = args.Blocks
	}
	if args.Owner != "" {
		existing.Owner = args.Owner
	}

	if err := GlobalTaskManager.SaveTask(existing); err != nil {
		return TaskUpdateResult{Success: false, Message: err.Error()}, WrapToolError("task_update", args, err)
	}
	return TaskUpdateResult{Success: true, Message: fmt.Sprintf("✅ 任务已更新: %s", args.ID)}, nil
}

// TaskListArgs representing arguments for task_list.
type TaskListArgs struct{}

type TaskListResult struct {
	Tasks []*TaskRecord `json:"tasks"`
}

func TaskListHandler(ctx tool.Context, args TaskListArgs) (TaskListResult, error) {
	tasks, err := GlobalTaskManager.ListTasks()
	if err != nil {
		return TaskListResult{}, WrapToolError("task_list", args, err)
	}
	return TaskListResult{Tasks: tasks}, nil
}

// TaskGetArgs representing arguments for task_get.
type TaskGetArgs struct {
	ID string `json:"id" description:"任务唯一标识"`
}

type TaskGetResult struct {
	Task *TaskRecord `json:"task"`
}

func TaskGetHandler(ctx tool.Context, args TaskGetArgs) (TaskGetResult, error) {
	task, err := GlobalTaskManager.GetTask(args.ID)
	if err != nil {
		return TaskGetResult{}, WrapToolError("task_get", args, err)
	}
	return TaskGetResult{Task: task}, nil
}

// BackgroundRunArgs represents arguments for background_run.
type BackgroundRunArgs struct {
	Command string `json:"command" description:"要在后台线程执行的 Shell 命令。立刻返回 task_id。"`
}

type BackgroundRunResult struct {
	Message string `json:"message"`
}

func BackgroundRunHandler(ctx tool.Context, args BackgroundRunArgs) (BackgroundRunResult, error) {
	msg, err := GlobalBackgroundManager.Run(args.Command)
	if err != nil {
		return BackgroundRunResult{}, WrapToolError("background_run", args, err)
	}
	return BackgroundRunResult{Message: msg}, nil
}

// CheckBackgroundArgs represents arguments for check_background.
type CheckBackgroundArgs struct {
	TaskID string `json:"task_id,omitempty" description:"可选。特定后台任务 ID。如果省略，列出所有后台任务状态。"`
}

type CheckBackgroundResult struct {
	Output string `json:"output"`
}

func CheckBackgroundHandler(ctx tool.Context, args CheckBackgroundArgs) (CheckBackgroundResult, error) {
	out, err := GlobalBackgroundManager.Check(args.TaskID)
	if err != nil {
		return CheckBackgroundResult{}, WrapToolError("check_background", args, err)
	}
	return CheckBackgroundResult{Output: out}, nil
}

// ─── Schedule tool handlers ───────────────────────────────────────────────

type ScheduleCreateArgs struct {
	CronExpr  string `json:"cron_expr" description:"5位标准 Cron 表达式，如 '*/5 * * * *'"`
	Prompt    string `json:"prompt" description:"触发时自动追加给大模型的指令文本"`
	Recurring bool   `json:"recurring" description:"是否为循环任务，若为 false 则触发一次后自动销毁"`
	Durable   bool   `json:"durable" description:"是否持久化到磁盘，若为 true 则在 CLI 重启后仍会恢复执行"`
}

type ScheduleCreateResult struct {
	Message string `json:"message"`
}

func ScheduleCreateHandler(ctx tool.Context, args ScheduleCreateArgs) (ScheduleCreateResult, error) {
	msg, err := GlobalCronScheduler.Create(args.CronExpr, args.Prompt, args.Recurring, args.Durable)
	if err != nil {
		return ScheduleCreateResult{}, WrapToolError("schedule_create", args, err)
	}
	return ScheduleCreateResult{Message: msg}, nil
}

type ScheduleListArgs struct{}

type ScheduleListResult struct {
	ActiveTasks string `json:"active_tasks"`
}

func ScheduleListHandler(ctx tool.Context, args ScheduleListArgs) (ScheduleListResult, error) {
	out := GlobalCronScheduler.ListTasks()
	return ScheduleListResult{ActiveTasks: out}, nil
}

type ScheduleDeleteArgs struct {
	TaskID string `json:"task_id" description:"要删除的调度任务 ID"`
}

type ScheduleDeleteResult struct {
	Message string `json:"message"`
}

func ScheduleDeleteHandler(ctx tool.Context, args ScheduleDeleteArgs) (ScheduleDeleteResult, error) {
	msg, err := GlobalCronScheduler.Delete(args.TaskID)
	if err != nil {
		return ScheduleDeleteResult{}, WrapToolError("schedule_delete", args, err)
	}
	return ScheduleDeleteResult{Message: msg}, nil
}

// ─── Team tool handlers (s15) ──────────────────────────────────────────────

type SpawnTeammateArgs struct {
	Name         string `json:"name" description:"特工代理人唯一名称"`
	Role         string `json:"role" description:"负责分工的角色，如 database, frontend"`
	SystemPrompt string `json:"system_prompt" description:"系统指令"`
}

type SpawnTeammateResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func SpawnTeammateHandler(ctx tool.Context, args SpawnTeammateArgs) (SpawnTeammateResult, error) {
	_, err := GlobalTeamManager.RegisterTeammate(args.Name, args.Role, args.SystemPrompt)
	if err != nil {
		return SpawnTeammateResult{Success: false}, WrapToolError("spawn_teammate", args, err)
	}
	err = GlobalTeamManager.StartTeammateLoop(args.Name)
	if err != nil {
		return SpawnTeammateResult{Success: false}, WrapToolError("spawn_teammate", args, err)
	}
	return SpawnTeammateResult{Success: true, Message: fmt.Sprintf("✅ 特工代理人 %s 已成功启动在后台", args.Name)}, nil
}

type ListTeammatesArgs struct{}

type ListTeammatesResult struct {
	Teammates []Teammate `json:"teammates"`
}

func ListTeammatesHandler(ctx tool.Context, args ListTeammatesArgs) (ListTeammatesResult, error) {
	list, err := GlobalTeamManager.ListTeammates()
	if err != nil {
		return ListTeammatesResult{}, WrapToolError("list_teammates", args, err)
	}
	return ListTeammatesResult{Teammates: list}, nil
}

type SendMessageArgs struct {
	Recipient string `json:"recipient" description:"接收消息的特工代理人名称"`
	Content   string `json:"content" description:"发送的消息文本内容"`
}

type SendMessageResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func SendMessageHandler(ctx tool.Context, args SendMessageArgs) (SendMessageResult, error) {
	msg := TeamMessage{
		Sender:    "agent",
		Content:   args.Content,
		Timestamp: float64(time.Now().Unix()),
	}
	err := GlobalTeamManager.AppendToInbox(args.Recipient, msg)
	if err != nil {
		return SendMessageResult{Success: false}, WrapToolError("send_message", args, err)
	}
	return SendMessageResult{Success: true, Message: fmt.Sprintf("✅ 消息已发送至 %s", args.Recipient)}, nil
}

type ReadInboxArgs struct {
	Name string `json:"name" description:"特工代理人的名称"`
}

type ReadInboxResult struct {
	Messages []TeamMessage `json:"messages"`
}

func ReadInboxHandler(ctx tool.Context, args ReadInboxArgs) (ReadInboxResult, error) {
	msgs, err := GlobalTeamManager.ReadAndClearInbox(args.Name)
	if err != nil {
		return ReadInboxResult{}, WrapToolError("read_inbox", args, err)
	}
	return ReadInboxResult{Messages: msgs}, nil
}

type BroadcastArgs struct {
	Content string `json:"content" description:"广播至全体特工的文本消息"`
}

type BroadcastResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func BroadcastHandler(ctx tool.Context, args BroadcastArgs) (BroadcastResult, error) {
	err := GlobalTeamManager.Broadcast("agent", args.Content)
	if err != nil {
		return BroadcastResult{Success: false}, WrapToolError("broadcast", args, err)
	}
	return BroadcastResult{Success: true, Message: "✅ 广播消息已发送给所有特工成员"}, nil
}

// ─── Protocol tool handlers (s16) ──────────────────────────────────────────

type ProtocolShutdownRequestArgs struct {
	Sender   string `json:"sender" description:"请求的发起特工名称"`
	Receiver string `json:"receiver" description:"接收请求的特工名称"`
	Reason   string `json:"reason" description:"请求停机的缘由说明"`
}

type ProtocolShutdownRequestResult struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

func ProtocolShutdownRequestHandler(ctx tool.Context, args ProtocolShutdownRequestArgs) (ProtocolShutdownRequestResult, error) {
	req, err := GlobalProtocolManager.CreateRequest("shutdown", args.Sender, args.Receiver, map[string]any{"reason": args.Reason})
	if err != nil {
		return ProtocolShutdownRequestResult{}, WrapToolError("protocol_shutdown_request", args, err)
	}
	return ProtocolShutdownRequestResult{RequestID: req.RequestID, Status: req.Status}, nil
}

type ProtocolShutdownResponseArgs struct {
	RequestID string `json:"request_id" description:"待确认的停机请求 ID"`
	Approved  bool   `json:"approved" description:"是否同意停机"`
	Comment   string `json:"comment,omitempty" description:"审批评语"`
}

type ProtocolShutdownResponseResult struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

func ProtocolShutdownResponseHandler(ctx tool.Context, args ProtocolShutdownResponseArgs) (ProtocolShutdownResponseResult, error) {
	req, err := GlobalProtocolManager.RespondToRequest(args.RequestID, args.Approved, args.Comment)
	if err != nil {
		return ProtocolShutdownResponseResult{}, WrapToolError("protocol_shutdown_response", args, err)
	}
	return ProtocolShutdownResponseResult{Success: true, Status: req.Status}, nil
}

type ProtocolPlanApprovalRequestArgs struct {
	Sender   string `json:"sender" description:"发起方案审批的特工名称"`
	Receiver string `json:"receiver" description:"负责审批方案的特工名称"`
	Plan     string `json:"plan" description:"拟审批的详细方案或步骤清单"`
}

type ProtocolPlanApprovalRequestResult struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

func ProtocolPlanApprovalRequestHandler(ctx tool.Context, args ProtocolPlanApprovalRequestArgs) (ProtocolPlanApprovalRequestResult, error) {
	req, err := GlobalProtocolManager.CreateRequest("plan_approval", args.Sender, args.Receiver, map[string]any{"plan": args.Plan})
	if err != nil {
		return ProtocolPlanApprovalRequestResult{}, WrapToolError("protocol_plan_approval_request", args, err)
	}
	return ProtocolPlanApprovalRequestResult{RequestID: req.RequestID, Status: req.Status}, nil
}

type ProtocolPlanApprovalResponseArgs struct {
	RequestID string `json:"request_id" description:"待审批的方案请求 ID"`
	Approved  bool   `json:"approved" description:"是否批准此方案"`
	Comment   string `json:"comment,omitempty" description:"修改意见或评语"`
}

type ProtocolPlanApprovalResponseResult struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

func ProtocolPlanApprovalResponseHandler(ctx tool.Context, args ProtocolPlanApprovalResponseArgs) (ProtocolPlanApprovalResponseResult, error) {
	req, err := GlobalProtocolManager.RespondToRequest(args.RequestID, args.Approved, args.Comment)
	if err != nil {
		return ProtocolPlanApprovalResponseResult{}, WrapToolError("protocol_plan_approval_response", args, err)
	}
	return ProtocolPlanApprovalResponseResult{Success: true, Status: req.Status}, nil
}

// ─── Autonomous agent tool handlers (s17) ──────────────────────────────────

type AgentClaimTaskArgs struct {
	TeammateName string   `json:"teammate_name" description:"声明认领任务的特工名称"`
	Keywords     []string `json:"keywords" description:"匹配任务标题的主题关键字列表"`
}

type AgentClaimTaskResult struct {
	ClaimedTasks []string `json:"claimed_tasks"`
}

func AgentClaimTaskHandler(ctx tool.Context, args AgentClaimTaskArgs) (AgentClaimTaskResult, error) {
	claimed, err := GlobalAutonomyManager.AutoClaimTasks(args.TeammateName, args.Keywords)
	if err != nil {
		return AgentClaimTaskResult{}, WrapToolError("agent_claim_task", args, err)
	}
	return AgentClaimTaskResult{ClaimedTasks: claimed}, nil
}

type AgentSetStateArgs struct {
	State string `json:"state" description:"状态，可选：WORK, IDLE"`
}

type AgentSetStateResult struct {
	Success bool   `json:"success"`
	State   string `json:"state"`
}

func AgentSetStateHandler(ctx tool.Context, args AgentSetStateArgs) (AgentSetStateResult, error) {
	s := AgentState(args.State)
	if s != StateWork && s != StateIdle {
		return AgentSetStateResult{Success: false}, fmt.Errorf("invalid agent state: %s (must be WORK or IDLE)", args.State)
	}
	GlobalAutonomyManager.SetState(s)
	return AgentSetStateResult{Success: true, State: string(s)}, nil
}

// ─── Worktree tool handlers (s18) ──────────────────────────────────────────

type WorktreeCreateArgs struct {
	Name   string `json:"name" description:"隔离工作区分支名称，如 wt-feat-auth"`
	TaskID string `json:"task_id" description:"绑定的任务 ID"`
}

type WorktreeCreateResult struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
}

func WorktreeCreateHandler(ctx tool.Context, args WorktreeCreateArgs) (WorktreeCreateResult, error) {
	entry, err := GlobalWorktreeManager.Create(args.Name, args.TaskID)
	if err != nil {
		return WorktreeCreateResult{Success: false}, WrapToolError("worktree_create", args, err)
	}
	return WorktreeCreateResult{Success: true, Path: entry.Path, Branch: entry.Branch}, nil
}

type WorktreeListArgs struct{}

type WorktreeListResult struct {
	Worktrees []WorktreeEntry `json:"worktrees"`
}

func WorktreeListHandler(ctx tool.Context, args WorktreeListArgs) (WorktreeListResult, error) {
	list, err := GlobalWorktreeManager.List()
	if err != nil {
		return WorktreeListResult{}, WrapToolError("worktree_list", args, err)
	}
	return WorktreeListResult{Worktrees: list}, nil
}

type WorktreeStatusArgs struct {
	Name string `json:"name" description:"待查询的隔离区名称"`
}

type WorktreeStatusResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	TaskID string `json:"task_id"`
}

func WorktreeStatusHandler(ctx tool.Context, args WorktreeStatusArgs) (WorktreeStatusResult, error) {
	_ = GlobalWorktreeManager.LoadIndex()
	GlobalWorktreeManager.mu.RLock()
	entry, ok := GlobalWorktreeManager.entries[args.Name]
	GlobalWorktreeManager.mu.RUnlock()

	if !ok {
		return WorktreeStatusResult{}, fmt.Errorf("worktree '%s' not found", args.Name)
	}
	return WorktreeStatusResult{Name: entry.Name, Status: entry.Status, TaskID: entry.TaskID}, nil
}

type WorktreeEnterArgs struct {
	Name string `json:"name" description:"要切换并进入的隔离区名称"`
}

type WorktreeEnterResult struct {
	Success bool `json:"success"`
}

func WorktreeEnterHandler(ctx tool.Context, args WorktreeEnterArgs) (WorktreeEnterResult, error) {
	err := GlobalWorktreeManager.Enter(args.Name)
	if err != nil {
		return WorktreeEnterResult{Success: false}, WrapToolError("worktree_enter", args, err)
	}
	return WorktreeEnterResult{Success: true}, nil
}

type WorktreeCloseoutArgs struct {
	Name         string `json:"name" description:"隔离区名称"`
	Action       string `json:"action" description:"收尾操作类型，可选：keep, remove"`
	CompleteTask bool   `json:"complete_task" description:"是否同时联动将绑定的任务状态标记为已完成"`
}

type WorktreeCloseoutResult struct {
	Success bool `json:"success"`
}

func WorktreeCloseoutHandler(ctx tool.Context, args WorktreeCloseoutArgs) (WorktreeCloseoutResult, error) {
	err := GlobalWorktreeManager.Closeout(args.Name, args.Action, args.CompleteTask)
	if err != nil {
		return WorktreeCloseoutResult{Success: false}, WrapToolError("worktree_closeout", args, err)
	}
	return WorktreeCloseoutResult{Success: true}, nil
}

// ─── MCP tool handlers (s19) ───────────────────────────────────────────────

type MCPServerListArgs struct{}

type MCPServerListResult struct {
	Servers map[string]string `json:"servers"`
}

func MCPServerListHandler(ctx tool.Context, args MCPServerListArgs) (MCPServerListResult, error) {
	list := GlobalMCPRouter.ListServers()
	return MCPServerListResult{Servers: list}, nil
}
