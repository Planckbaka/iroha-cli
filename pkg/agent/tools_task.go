package agent

import (
	"fmt"

	"google.golang.org/adk/tool"
)

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
