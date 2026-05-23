package agent

import (
	"fmt"

	"google.golang.org/adk/tool"
)

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
