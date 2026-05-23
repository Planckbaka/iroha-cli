package agent

import (
	"google.golang.org/adk/tool"
)

type ScheduleCreateArgs struct {
	CronExpr  string `json:"cron_expr" description:"5位标准 Cron 表达式，如 '*/5 * * * *'"`
	Prompt    string `json:"prompt" description:"触发时自动追加给大模型的指令文本"`
	Recurring bool   `json:"recurring" description:"是否为循环任务，若为 false 则触发一次后自动销毁"`
	Durable   bool   `json:"durable" description:"是否持久化到磁盘，若为 true 则在 CLI 重启后仍会恢复执行"`
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
	TaskID string `json:"task_id" description:"要删除的调度任务 ID"`
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
