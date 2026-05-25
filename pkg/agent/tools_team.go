package agent

import (
	"fmt"
	"time"

	"google.golang.org/adk/tool"
)

type SpawnTeammateArgs struct {
	Name         string `json:"name" description:"Unique teammate agent name"`
	Role         string `json:"role" description:"Role assignment, e.g. database, frontend"`
	AgentType    string `json:"agent_type" description:"Agent type: explore, planner, reviewer, executor, researcher"`
	SystemPrompt string `json:"system_prompt" description:"System instructions"`
}

type SpawnTeammateResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func SpawnTeammateHandler(ctx tool.Context, args SpawnTeammateArgs) (SpawnTeammateResult, error) {
	_, err := GlobalTeamManager.RegisterTeammate(args.Name, args.Role, args.SystemPrompt, args.AgentType)
	if err != nil {
		return SpawnTeammateResult{Success: false}, WrapToolError("spawn_teammate", args, err)
	}
	err = GlobalTeamManager.StartTeammateLoop(args.Name)
	if err != nil {
		return SpawnTeammateResult{Success: false}, WrapToolError("spawn_teammate", args, err)
	}
	return SpawnTeammateResult{Success: true, Message: fmt.Sprintf("Teammate %s successfully started in background", args.Name)}, nil
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
	Recipient string `json:"recipient" description:"Name of the teammate agent to receive the message"`
	Content   string `json:"content" description:"Message text content to send"`
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
	return SendMessageResult{Success: true, Message: fmt.Sprintf("Message sent to %s", args.Recipient)}, nil
}

type ReadInboxArgs struct {
	Name string `json:"name" description:"Teammate agent name"`
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
	Content string `json:"content" description:"Text message to broadcast to all teammates"`
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
	return BroadcastResult{Success: true, Message: "Broadcast message sent to all teammates"}, nil
}

// ─── Protocol tool handlers (s16) ──────────────────────────────────────────

type ProtocolShutdownRequestArgs struct {
	Sender   string `json:"sender" description:"Name of the teammate initiating the request"`
	Receiver string `json:"receiver" description:"Name of the teammate receiving the request"`
	Reason   string `json:"reason" description:"Reason for the shutdown request"`
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
	RequestID string `json:"request_id" description:"The shutdown request ID to confirm"`
	Approved  bool   `json:"approved" description:"Whether to approve the shutdown"`
	Comment   string `json:"comment,omitempty" description:"Approval comment"`
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
	Sender   string `json:"sender" description:"Name of the teammate submitting the plan for approval"`
	Receiver string `json:"receiver" description:"Name of the teammate responsible for approving the plan"`
	Plan     string `json:"plan" description:"Detailed plan or step list to be approved"`
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
	RequestID string `json:"request_id" description:"The plan approval request ID to respond to"`
	Approved  bool   `json:"approved" description:"Whether to approve this plan"`
	Comment   string `json:"comment,omitempty" description:"Revision feedback or comment"`
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
	TeammateName string   `json:"teammate_name" description:"Name of the teammate claiming tasks"`
	Keywords     []string `json:"keywords" description:"List of topic keywords to match against task titles"`
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
	State string `json:"state" description:"Agent state. Options: WORK, IDLE"`
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
