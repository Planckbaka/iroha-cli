package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"google.golang.org/adk/tool"
)

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
	if err := checkShellCommandSandbox(args.Command); err != nil {
		return ShellRunResult{}, err
	}

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

// BackgroundRunArgs represents arguments for background_run.
type BackgroundRunArgs struct {
	Command string `json:"command" description:"要在后台线程执行的 Shell 命令。立刻返回 task_id。"`
}

type BackgroundRunResult struct {
	Message string `json:"message"`
}

func BackgroundRunHandler(ctx tool.Context, args BackgroundRunArgs) (BackgroundRunResult, error) {
	if err := checkShellCommandSandbox(args.Command); err != nil {
		return BackgroundRunResult{}, err
	}

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
