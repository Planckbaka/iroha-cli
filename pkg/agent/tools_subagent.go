package agent

import (
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/adk/tool"
)

// SubagentRunArgs represents arguments for the subagent_run tool.
type SubagentRunArgs struct {
	Prompt    string `json:"prompt" description:"The specific instruction/task for the subagent to perform."`
	ModelName string `json:"model_name,omitempty" description:"Optional. The model to use for the subagent (e.g. 'deepseek-chat', 'claude-3-5-haiku'). If omitted, defaults to the current session model."`
}

// SubagentRunResult represents the output returned by the subagent.
type SubagentRunResult struct {
	Summary string `json:"summary" description:"The summary of findings and results returned by the subagent."`
}

// SubagentRunHandler dynamically spawns a stateless subagent in the current workspace directory.
func SubagentRunHandler(ctx tool.Context, args SubagentRunArgs) (SubagentRunResult, error) {
	subagentName := "subagent-" + uuid.New().String()[:8]

	teammate := &Teammate{
		Name:         subagentName,
		Type:         "executor",
		SystemPrompt: "You are a specialized subagent. Complete the task as instructed. Return a clear and concise summary of your findings and results. Bypassing confirmation is NOT allowed; all edits and commands will prompt the user for permission.",
	}

	GlobalAgentPool.mu.RLock()
	originalModel := GlobalAgentPool.ModelName
	GlobalAgentPool.mu.RUnlock()

	if args.ModelName != "" {
		GlobalAgentPool.mu.Lock()
		GlobalAgentPool.ModelName = args.ModelName
		GlobalAgentPool.mu.Unlock()

		defer func() {
			GlobalAgentPool.mu.Lock()
			GlobalAgentPool.ModelName = originalModel
			GlobalAgentPool.mu.Unlock()
		}()
	}

	parentWorkdir := getWorkdir(ctx)
	msg := TeamMessage{
		Sender:    "parent",
		Content:   args.Prompt,
	}

	// Send status update to TUI
	ToolBridge.Send(ToolStatus{
		Name:    subagentName,
		Args:    args,
		Running: true,
	})

	summary, err := GlobalAgentPool.ExecuteMessageInDir(teammate, msg, parentWorkdir)

	ToolBridge.Send(ToolStatus{
		Name:    subagentName,
		Args:    args,
		Running: false,
		Success: err == nil,
		Error:   err,
	})

	if err != nil {
		return SubagentRunResult{}, fmt.Errorf("subagent failed: %w", err)
	}

	return SubagentRunResult{
		Summary: summary,
	}, nil
}
