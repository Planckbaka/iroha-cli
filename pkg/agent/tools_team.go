package agent

import (
	"fmt"
	"time"

	"google.golang.org/adk/tool"
)

type SpawnTeammateArgs struct {
	Name         string `json:"name" description:"特工代理人唯一名称"`
	Role         string `json:"role" description:"负责分工的角色，如 database, frontend"`
	SystemPrompt string `json:"system_prompt" description:"系统指令"`
}

type SpawnTeammateResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func SpawnTeammateHandler(ctx tool.Context, args SpawnTeammateArgs) (SpawnTeammateResult, error) {
	_, err := GlobalTeamManager.RegisterTeammate(args.Name, args.Role, args.SystemPrompt)
	if err != nil {
		return SpawnTeammateResult{Success: false}, WrapToolError("spawn_teammate", args, err)
	}
	err = GlobalTeamManager.StartTeammateLoop(args.Name)
	if err != nil {
		return SpawnTeammateResult{Success: false}, WrapToolError("spawn_teammate", args, err)
	}
	return SpawnTeammateResult{Success: true, Message: fmt.Sprintf("✅ 特工代理人 %s 已成功启动在后台", args.Name)}, nil
}

type ListTeammatesArgs struct{}

type ListTeammatesResult struct {
	Teammates []Teammate `json:"teammates"`
}

func ListTeammatesHandler(ctx tool.Context, args ListTeammatesArgs) (ListTeammatesResult, error) {
	list, err := GlobalTeamManager.ListTeammates()
	if err != nil {
		return ListTeammatesResult{}, WrapToolError("list_teammates", args, err)
	}
	return ListTeammatesResult{Teammates: list}, nil
}

type SendMessageArgs struct {
	Recipient string `json:"recipient" description:"接收消息的特工代理人名称"`
	Content   string `json:"content" description:"发送的消息文本内容"`
}

type SendMessageResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func SendMessageHandler(ctx tool.Context, args SendMessageArgs) (SendMessageResult, error) {
	msg := TeamMessage{
		Sender:    "agent",
		Content:   args.Content,
		Timestamp: float64(time.Now().Unix()),
	}
	err := GlobalTeamManager.AppendToInbox(args.Recipient, msg)
	if err != nil {
		return SendMessageResult{Success: false}, WrapToolError("send_message", args, err)
	}
	return SendMessageResult{Success: true, Message: fmt.Sprintf("✅ 消息已发送至 %s", args.Recipient)}, nil
}

type ReadInboxArgs struct {
	Name string `json:"name" description:"特工代理人的名称"`
}

type ReadInboxResult struct {
	Messages []TeamMessage `json:"messages"`
}

func ReadInboxHandler(ctx tool.Context, args ReadInboxArgs) (ReadInboxResult, error) {
	msgs, err := GlobalTeamManager.ReadAndClearInbox(args.Name)
	if err != nil {
		return ReadInboxResult{}, WrapToolError("read_inbox", args, err)
	}
	return ReadInboxResult{Messages: msgs}, nil
}

type BroadcastArgs struct {
	Content string `json:"content" description:"广播至全体特工的文本消息"`
}

type BroadcastResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func BroadcastHandler(ctx tool.Context, args BroadcastArgs) (BroadcastResult, error) {
	err := GlobalTeamManager.Broadcast("agent", args.Content)
	if err != nil {
		return BroadcastResult{Success: false}, WrapToolError("broadcast", args, err)
	}
	return BroadcastResult{Success: true, Message: "✅ 广播消息已发送给所有特工成员"}, nil
}

// ─── Protocol tool handlers (s16) ──────────────────────────────────────────

type ProtocolShutdownRequestArgs struct {
	Sender   string `json:"sender" description:"请求的发起特工名称"`
	Receiver string `json:"receiver" description:"接收请求的特工名称"`
	Reason   string `json:"reason" description:"请求停机的缘由说明"`
}

type ProtocolShutdownRequestResult struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

func ProtocolShutdownRequestHandler(ctx tool.Context, args ProtocolShutdownRequestArgs) (ProtocolShutdownRequestResult, error) {
	req, err := GlobalProtocolManager.CreateRequest("shutdown", args.Sender, args.Receiver, map[string]any{"reason": args.Reason})
	if err != nil {
		return ProtocolShutdownRequestResult{}, WrapToolError("protocol_shutdown_request", args, err)
	}
	return ProtocolShutdownRequestResult{RequestID: req.RequestID, Status: req.Status}, nil
}

type ProtocolShutdownResponseArgs struct {
	RequestID string `json:"request_id" description:"待确认的停机请求 ID"`
	Approved  bool   `json:"approved" description:"是否同意停机"`
	Comment   string `json:"comment,omitempty" description:"审批评语"`
}

type ProtocolShutdownResponseResult struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

func ProtocolShutdownResponseHandler(ctx tool.Context, args ProtocolShutdownResponseArgs) (ProtocolShutdownResponseResult, error) {
	req, err := GlobalProtocolManager.RespondToRequest(args.RequestID, args.Approved, args.Comment)
	if err != nil {
		return ProtocolShutdownResponseResult{}, WrapToolError("protocol_shutdown_response", args, err)
	}
	return ProtocolShutdownResponseResult{Success: true, Status: req.Status}, nil
}

type ProtocolPlanApprovalRequestArgs struct {
	Sender   string `json:"sender" description:"发起方案审批的特工名称"`
	Receiver string `json:"receiver" description:"负责审批方案的特工名称"`
	Plan     string `json:"plan" description:"拟审批的详细方案或步骤清单"`
}

type ProtocolPlanApprovalRequestResult struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

func ProtocolPlanApprovalRequestHandler(ctx tool.Context, args ProtocolPlanApprovalRequestArgs) (ProtocolPlanApprovalRequestResult, error) {
	req, err := GlobalProtocolManager.CreateRequest("plan_approval", args.Sender, args.Receiver, map[string]any{"plan": args.Plan})
	if err != nil {
		return ProtocolPlanApprovalRequestResult{}, WrapToolError("protocol_plan_approval_request", args, err)
	}
	return ProtocolPlanApprovalRequestResult{RequestID: req.RequestID, Status: req.Status}, nil
}

type ProtocolPlanApprovalResponseArgs struct {
	RequestID string `json:"request_id" description:"待审批的方案请求 ID"`
	Approved  bool   `json:"approved" description:"是否批准此方案"`
	Comment   string `json:"comment,omitempty" description:"修改意见或评语"`
}

type ProtocolPlanApprovalResponseResult struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

func ProtocolPlanApprovalResponseHandler(ctx tool.Context, args ProtocolPlanApprovalResponseArgs) (ProtocolPlanApprovalResponseResult, error) {
	req, err := GlobalProtocolManager.RespondToRequest(args.RequestID, args.Approved, args.Comment)
	if err != nil {
		return ProtocolPlanApprovalResponseResult{}, WrapToolError("protocol_plan_approval_response", args, err)
	}
	return ProtocolPlanApprovalResponseResult{Success: true, Status: req.Status}, nil
}

// ─── Autonomous agent tool handlers (s17) ──────────────────────────────────

type AgentClaimTaskArgs struct {
	TeammateName string   `json:"teammate_name" description:"声明认领任务的特工名称"`
	Keywords     []string `json:"keywords" description:"匹配任务标题的主题关键字列表"`
}

type AgentClaimTaskResult struct {
	ClaimedTasks []string `json:"claimed_tasks"`
}

func AgentClaimTaskHandler(ctx tool.Context, args AgentClaimTaskArgs) (AgentClaimTaskResult, error) {
	claimed, err := GlobalAutonomyManager.AutoClaimTasks(args.TeammateName, args.Keywords)
	if err != nil {
		return AgentClaimTaskResult{}, WrapToolError("agent_claim_task", args, err)
	}
	return AgentClaimTaskResult{ClaimedTasks: claimed}, nil
}

type AgentSetStateArgs struct {
	State string `json:"state" description:"状态，可选：WORK, IDLE"`
}

type AgentSetStateResult struct {
	Success bool   `json:"success"`
	State   string `json:"state"`
}

func AgentSetStateHandler(ctx tool.Context, args AgentSetStateArgs) (AgentSetStateResult, error) {
	s := AgentState(args.State)
	if s != StateWork && s != StateIdle {
		return AgentSetStateResult{Success: false}, fmt.Errorf("invalid agent state: %s (must be WORK or IDLE)", args.State)
	}
	GlobalAutonomyManager.SetState(s)
	return AgentSetStateResult{Success: true, State: string(s)}, nil
}
