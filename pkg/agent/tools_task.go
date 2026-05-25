package agent

import (
	"fmt"

	"google.golang.org/adk/tool"
)

// TaskCreateArgs represents arguments for task_create tool.
type TaskCreateArgs struct {
	ID          string   `json:"id" description:"Unique task identifier, e.g. t1, task-setup"`
	Subject     string   `json:"subject" description:"Task subject or short summary"`
	Description string   `json:"description,omitempty" description:"Detailed task description"`
	Status      string   `json:"status,omitempty" description:"Task status, defaults to pending. Options: pending, in_progress, completed"`
	BlockedBy   []string `json:"blockedBy,omitempty" description:"List of upstream dependency task IDs"`
	Blocks      []string `json:"blocks,omitempty" description:"List of downstream task IDs blocked by this task"`
	Owner       string   `json:"owner,omitempty" description:"Task owner, defaults to agent. Options: agent, user"`
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
	return TaskCreateResult{Success: true, Message: fmt.Sprintf("Task created: %s", task.ID)}, nil
}

// TaskUpdateArgs represents arguments for task_update tool.
type TaskUpdateArgs struct {
	ID          string   `json:"id" description:"Unique task identifier to update"`
	Subject     string   `json:"subject,omitempty" description:"New task subject"`
	Description string   `json:"description,omitempty" description:"New task description"`
	Status      string   `json:"status,omitempty" description:"New task status. Options: pending, in_progress, completed, deleted"`
	BlockedBy   []string `json:"blockedBy,omitempty" description:"New upstream dependency task ID list (if provided, fully replaces the existing list)"`
	Blocks      []string `json:"blocks,omitempty" description:"New downstream dependency task ID list (if provided, fully replaces the existing list)"`
	Owner       string   `json:"owner,omitempty" description:"New task owner"`
}

type TaskUpdateResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func TaskUpdateHandler(ctx tool.Context, args TaskUpdateArgs) (TaskUpdateResult, error) {
	existing, err := GlobalTaskManager.GetTask(args.ID)
	if err != nil {
		return TaskUpdateResult{Success: false, Message: fmt.Sprintf("Task not found: %s", args.ID)}, WrapToolError("task_update", args, err)
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
	return TaskUpdateResult{Success: true, Message: fmt.Sprintf("Task updated: %s", args.ID)}, nil
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
	ID string `json:"id" description:"Unique task identifier"`
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
