package agent

import (
	"fmt"

	"google.golang.org/adk/tool"
)

// 5. todo (session progress planning tool)
type TodoArgs struct {
	Items []TodoItem `json:"items" description:"The full plan list to update"`
}

type TodoResult struct {
	RenderedPlan string `json:"rendered_plan" description:"Formatted rendered current progress board"`
}

func TodoHandler(ctx tool.Context, args TodoArgs) (TodoResult, error) {
	err := GlobalTodoManager.Update(args.Items)
	if err != nil {
		return TodoResult{}, WrapToolError("todo", args, fmt.Errorf("failed to update task list: %w", err))
	}
	return TodoResult{RenderedPlan: GlobalTodoManager.Render()}, nil
}

func registerTodoTools(r *ToolRegistry) {
	register(r, "todo", "Rewrite or update the session-level plan list for the current multi-step task. Always call this tool first to create a plan for complex multi-step tasks, and update it when completing or starting steps. Exactly one task must be in the in_progress state at all times.", TodoHandler)
}
