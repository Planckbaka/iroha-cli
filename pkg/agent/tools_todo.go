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
		return TodoResult{}, fmt.Errorf("failed to update task list: %w", err)
	}
	return TodoResult{RenderedPlan: GlobalTodoManager.Render()}, nil
}
