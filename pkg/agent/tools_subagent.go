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
