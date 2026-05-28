package agent

import (
	"fmt"

	"google.golang.org/adk/tool"
)

type WorktreeCreateArgs struct {
	Name   string `json:"name" description:"Isolated workspace branch name, e.g. wt-feat-auth"`
	TaskID string `json:"task_id" description:"Bound task ID"`
}

type WorktreeCreateResult struct {
	Success bool   `json:"success"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
}

func WorktreeCreateHandler(ctx tool.Context, args WorktreeCreateArgs) (WorktreeCreateResult, error) {
	entry, err := GlobalWorktreeManager.Create(args.Name, args.TaskID)
	if err != nil {
		return WorktreeCreateResult{Success: false}, WrapToolError("worktree_create", args, err)
	}
	return WorktreeCreateResult{Success: true, Path: entry.Path, Branch: entry.Branch}, nil
}

type WorktreeListArgs struct{}

type WorktreeListResult struct {
	Worktrees []WorktreeEntry `json:"worktrees"`
}

func WorktreeListHandler(ctx tool.Context, args WorktreeListArgs) (WorktreeListResult, error) {
	list, err := GlobalWorktreeManager.List()
	if err != nil {
		return WorktreeListResult{}, WrapToolError("worktree_list", args, err)
	}
	return WorktreeListResult{Worktrees: list}, nil
}

type WorktreeStatusArgs struct {
	Name string `json:"name" description:"The isolated workspace name to query"`
}

type WorktreeStatusResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	TaskID string `json:"task_id"`
}

func WorktreeStatusHandler(ctx tool.Context, args WorktreeStatusArgs) (WorktreeStatusResult, error) {
	_ = GlobalWorktreeManager.LoadIndex()
	GlobalWorktreeManager.mu.RLock()
	entry, ok := GlobalWorktreeManager.entries[args.Name]
	GlobalWorktreeManager.mu.RUnlock()

	if !ok {
		return WorktreeStatusResult{}, WrapToolError("worktree_status", args, fmt.Errorf("worktree '%s' not found", args.Name))
	}
	return WorktreeStatusResult{Name: entry.Name, Status: entry.Status, TaskID: entry.TaskID}, nil
}

type WorktreeEnterArgs struct {
	Name string `json:"name" description:"The isolated workspace name to switch into"`
}

type WorktreeEnterResult struct {
	Success bool `json:"success"`
}

func WorktreeEnterHandler(ctx tool.Context, args WorktreeEnterArgs) (WorktreeEnterResult, error) {
	err := GlobalWorktreeManager.Enter(args.Name)
	if err != nil {
		return WorktreeEnterResult{Success: false}, WrapToolError("worktree_enter", args, err)
	}
	return WorktreeEnterResult{Success: true}, nil
}

type WorktreeCloseoutArgs struct {
	Name         string `json:"name" description:"Isolated workspace name"`
	Action       string `json:"action" description:"Close-out action type. Options: keep, remove"`
	CompleteTask bool   `json:"complete_task" description:"Whether to also mark the bound task as completed"`
}

type WorktreeCloseoutResult struct {
	Success bool `json:"success"`
}

func WorktreeCloseoutHandler(ctx tool.Context, args WorktreeCloseoutArgs) (WorktreeCloseoutResult, error) {
	err := GlobalWorktreeManager.Closeout(args.Name, args.Action, args.CompleteTask)
	if err != nil {
		return WorktreeCloseoutResult{Success: false}, WrapToolError("worktree_closeout", args, err)
	}
	return WorktreeCloseoutResult{Success: true}, nil
}

func registerWorktreeTools(r *ToolRegistry) {
	register(r, "worktree_create", "Create an isolated Git worktree branch for dedicated and safe development of a specific task.", WorktreeCreateHandler)
	register(r, "worktree_list", "List all current workspace isolation branches and directories.", WorktreeListHandler)
	register(r, "worktree_status", "Query the detailed status of a specific isolated workspace.", WorktreeStatusHandler)
	register(r, "worktree_enter", "Log entry into or activation of an isolated workspace.", WorktreeEnterHandler)
	register(r, "worktree_closeout", "Close out a specific isolated workspace branch. Choose to keep the path (keep) or forcefully remove it (remove).", WorktreeCloseoutHandler)
}
