package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go-claude/pkg/llm"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// AutoReviewResult represents the LLM's safety judgment for a shell command
type AutoReviewResult struct {
	Safe   bool   // true = safe to auto-approve, false = needs human review
	Reason string // human-readable explanation
}

// autoReviewConfig holds the LLM model for the safety reviewer
type autoReviewConfig struct {
	Model model.LLM
}

// GlobalAutoReviewConfig is set at startup from the LLM provider config
var GlobalAutoReviewConfig *autoReviewConfig

// SetAutoReviewConfig configures the auto-review LLM from runner startup
func SetAutoReviewConfig(m model.LLM) {
	GlobalAutoReviewConfig = &autoReviewConfig{
		Model: m,
	}
}

// autoReviewSystemPrompt is the safety judge system instruction
const autoReviewSystemPrompt = `你是一个严格的安全审查员，负责评估 Shell 命令是否安全。
你的任务是判断一条 Shell 命令是否可以在用户的工作区安全执行，无需用户手动审批。

判断标准：
- SAFE（安全，可自动放行）：只读操作，例如 ls, pwd, cat, echo, git status, git log, go build, go test, find, grep, head, tail, wc, which, env
- UNSAFE（危险，需要人工审核）：任何写入、删除、网络请求、系统修改、权限变更等操作

回复格式必须严格为 JSON：
{"safe": true, "reason": "只读查看目录，无风险"}
或
{"safe": false, "reason": "删除操作，可能造成数据丢失"}

只返回 JSON，不要任何额外文字。`

// ReviewCommand asks the configured LLM whether a shell command is safe.
// If no LLM is configured (simulate mode), falls back to heuristic judgment.
func ReviewCommand(cmd string) AutoReviewResult {
	if GlobalAutoReviewConfig == nil || GlobalAutoReviewConfig.Model == nil {
		return heuristicReview(cmd)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	result, err := callLLMForReview(ctx, GlobalAutoReviewConfig, cmd)
	if err != nil {
		return heuristicReview(cmd)
	}
	return result
}

// callLLMForReview makes a non-streaming LLM call for safety judgment via model.LLM
func callLLMForReview(ctx context.Context, cfg *autoReviewConfig, cmd string) (AutoReviewResult, error) {
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					{Text: fmt.Sprintf("请审查以下 Shell 命令：\n```\n%s\n```", cmd)},
				},
			},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Role: "system",
				Parts: []*genai.Part{
					{Text: autoReviewSystemPrompt},
				},
			},
		},
	}

	content, err := llm.CollectNonStreaming(ctx, cfg.Model, req)
	if err != nil {
		return AutoReviewResult{}, err
	}

	// Parse LLM JSON output
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result struct {
		Safe   bool   `json:"safe"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return AutoReviewResult{Safe: false, Reason: "AI 审查响应格式错误，交由人工确认"}, nil
	}

	return AutoReviewResult{Safe: result.Safe, Reason: result.Reason}, nil
}

// heuristicReview performs a fast rule-based safety check (no LLM needed)
// Used in simulate mode or when LLM call fails.
func heuristicReview(cmd string) AutoReviewResult {
	trimmed := strings.TrimSpace(strings.ToLower(cmd))

	// Check dangerous patterns FIRST — before safe commands
	dangerousPatterns := []string{
		"rm ", "rmdir", "mv ", "cp ", "chmod", "chown",
		"curl", "wget", "nc ", "ssh", "scp", "rsync",
		"sudo", "su ", "kill", "pkill",
		"dd ", "mkfs", "fdisk",
		">", ">>", "tee",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(trimmed, pattern) {
			return AutoReviewResult{
				Safe:   false,
				Reason: fmt.Sprintf("规则审查：命令含有危险模式 `%s`，需要人工确认", strings.TrimSpace(pattern)),
			}
		}
	}

	// Safe read-only commands (checked AFTER dangerous patterns)
	safeCommands := []string{
		"ls", "pwd", "cat", "echo", "head", "tail", "wc", "which", "env",
		"git status", "git log", "git diff", "git branch", "git remote",
		"go build", "go test", "go vet", "go list", "go env",
		"find", "grep", "rg", "fd", "tree",
		"date", "whoami", "hostname", "uname",
	}

	for _, safe := range safeCommands {
		if trimmed == safe || strings.HasPrefix(trimmed, safe+" ") {
			return AutoReviewResult{
				Safe:   true,
				Reason: fmt.Sprintf("规则审查：`%s` 是安全的只读命令", strings.Fields(trimmed)[0]),
			}
		}
	}

	// Unknown — ask human
	return AutoReviewResult{
		Safe:   false,
		Reason: "规则审查：未知命令，交由人工确认",
	}
}
