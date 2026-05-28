package agent

import (
	"google.golang.org/adk/tool"
)

// SpawnSubagentHandler processes synchronous subagent delegation requests
func SpawnSubagentHandler(ctx tool.Context, args SubagentSpec) (SubagentResult, error) {
	// Execute subagent synchronously
	res, err := GlobalSubagentManager.RunSubagent(ctx, args)
	if err != nil {
		return SubagentResult{Success: false}, WrapToolError("spawn_subagent", args, err)
	}
	return res, nil
}

func registerSubagentTools(r *ToolRegistry) {
	register(r, "spawn_subagent", "Spawn and run a synchronous subagent to execute a specific subtask (e.g. read, search, code review, or file changes) in an isolated, sandboxed execution scope and return a summary of findings.", SpawnSubagentHandler)
}
