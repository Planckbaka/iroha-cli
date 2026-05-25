package agent

import (
	"google.golang.org/adk/tool"
)

type ScheduleCreateArgs struct {
	CronExpr  string `json:"cron_expr" description:"Standard 5-field cron expression, e.g. '*/5 * * * *'"`
	Prompt    string `json:"prompt" description:"Instruction text to automatically feed to the LLM when triggered"`
	Recurring bool   `json:"recurring" description:"Whether this is a recurring task. If false, it self-destructs after one trigger"`
	Durable   bool   `json:"durable" description:"Whether to persist to disk. If true, the task survives CLI restarts"`
}

type ScheduleCreateResult struct {
	Message string `json:"message"`
}

func ScheduleCreateHandler(ctx tool.Context, args ScheduleCreateArgs) (ScheduleCreateResult, error) {
	msg, err := GlobalCronScheduler.Create(args.CronExpr, args.Prompt, args.Recurring, args.Durable)
	if err != nil {
		return ScheduleCreateResult{}, WrapToolError("schedule_create", args, err)
	}
	return ScheduleCreateResult{Message: msg}, nil
}

type ScheduleListArgs struct{}

type ScheduleListResult struct {
	ActiveTasks string `json:"active_tasks"`
}

func ScheduleListHandler(ctx tool.Context, args ScheduleListArgs) (ScheduleListResult, error) {
	out := GlobalCronScheduler.ListTasks()
	return ScheduleListResult{ActiveTasks: out}, nil
}

type ScheduleDeleteArgs struct {
	TaskID string `json:"task_id" description:"The scheduled task ID to delete"`
}

type ScheduleDeleteResult struct {
	Message string `json:"message"`
}

func ScheduleDeleteHandler(ctx tool.Context, args ScheduleDeleteArgs) (ScheduleDeleteResult, error) {
	msg, err := GlobalCronScheduler.Delete(args.TaskID)
	if err != nil {
		return ScheduleDeleteResult{}, WrapToolError("schedule_delete", args, err)
	}
	return ScheduleDeleteResult{Message: msg}, nil
}
