package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// 1. file_read
type FileReadArgs struct {
	Path string `json:"path" description:"要读取的文件路径（相对或绝对路径）"`
}

type FileReadResult struct {
	Content string `json:"content" description:"文件内容"`
}

func FileReadHandler(ctx tool.Context, args FileReadArgs) (FileReadResult, error) {
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

// 3. search_grep
type GrepArgs struct {
	Pattern string `json:"pattern" description:"正则表达式搜索模式"`
}

type GrepResult struct {
	Matches []string `json:"matches" description:"匹配到的行列表"`
}

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
		// Skip directories, .git, and build artifacts
		if info.IsDir() || containsAnyPath(path, []string{".git", "node_modules", "go.sum", "inspect.go"}) {
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
				if len(matches) >= 50 { // Limit to 50 results
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	return GrepResult{Matches: matches}, err
}

func containsAnyPath(path string, segments []string) bool {
	for _, s := range segments {
		if filepath.Base(path) == s || filepath.Join(path) == s {
			return true
		}
	}
	return false
}

// 4. shell_run (需要极其严格的人机确认)
type ShellRunArgs struct {
	Command string `json:"command" description:"要执行的本地 Shell 命令"`
}

type ShellRunResult struct {
	Output   string `json:"output" description:"命令的标准输出和标准错误输出合并内容"`
	ExitCode int    `json:"exit_code" description:"退出状态码"`
}

func ShellRunHandler(ctx tool.Context, args ShellRunArgs) (ShellRunResult, error) {
	cmd := exec.Command("sh", "-c", args.Command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	outputStr := out.String()
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

	return []tool.Tool{readTool, writeTool, grepTool, shellTool, todoTool, memorySaveTool, memoryListTool}, nil
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
