package agent

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Execute handles running a prompt asynchronously and piping events to a callback
func (cr *CustomRunner) Execute(ctx context.Context, userID, sessionID, prompt string, onEvent func(*session.Event), onError func(error), onDone func()) {
	cr.deps.ToolCircuitBreaker.Reset()
	cr.deps.Logger.SetSessionID(sessionID)

	LogAudit(CatUserInput, "user_prompt", "User submitted a prompt to the agent", map[string]any{
		"user_id":    userID,
		"session_id": sessionID,
		"prompt":     prompt,
	})

	// Reset the cancel channel for this execution turn
	cr.deps.Bridge.Reset()
	go func() {
		<-ctx.Done()
		cr.deps.Bridge.Cancel()
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				rollbackPendingEdits()
				err := fmt.Errorf("panic in agent execution: %v\n%s", r, debug.Stack())
				LogError(CatSystem, "runner_panic", "Agent execution panicked", err, map[string]any{
					"session_id": sessionID,
				})
				onError(err)
				onDone()
			}
		}()

		// Drain background task notifications
		bgNotifs := cr.deps.BackgroundManager.DrainNotifications()
		// Drain cron scheduler notifications
		cronNotifs := cr.deps.CronScheduler.DrainNotifications()

		var sb strings.Builder
		if len(bgNotifs) > 0 {
			sb.WriteString("<background-results>\n")
			for _, n := range bgNotifs {
				sb.WriteString(fmt.Sprintf("  <task id=\"%s\" status=\"%s\" command=\"%s\">\n", n.TaskID, n.Status, n.Command))
				sb.WriteString(fmt.Sprintf("    <preview>%s</preview>\n", n.Preview))
				sb.WriteString(fmt.Sprintf("    <output_file>%s</output_file>\n", n.OutputFile))
				sb.WriteString("  </task>\n")
			}
			sb.WriteString("</background-results>\n\n")
		}

		if len(cronNotifs) > 0 {
			sb.WriteString("<scheduled-results>\n")
			for _, n := range cronNotifs {
				missedAttr := ""
				if n.MissedAt != "" {
					missedAttr = fmt.Sprintf(" missed_at=\"%s\"", n.MissedAt)
				}
				sb.WriteString(fmt.Sprintf("  <trigger id=\"%s\"%s>\n", n.ScheduleID, missedAttr))
				sb.WriteString(fmt.Sprintf("    <prompt>%s</prompt>\n", n.Prompt))
				sb.WriteString("  </trigger>\n")
			}
			sb.WriteString("</scheduled-results>\n\n")
		}

		if sb.Len() > 0 {
			prompt = sb.String() + prompt
		}

		// Fire HookUserPrompt before sending to LLM
		hookUserResult := cr.deps.HookManager.RunHooks(HookUserPrompt, HookContext{
			Prompt:    prompt,
			SessionID: sessionID,
		})
		if hookUserResult.Blocked {
			LogAudit(CatUserInput, "user_prompt_blocked", "User prompt blocked by hook", map[string]any{
				"session_id": sessionID,
				"reason":     hookUserResult.BlockReason,
			})
			onError(fmt.Errorf("prompt blocked by hook: %s", hookUserResult.BlockReason))
			onDone()
			return
		}
		// If hooks injected messages, prepend them to the prompt
		if len(hookUserResult.Messages) > 0 {
			prompt = strings.Join(hookUserResult.Messages, "\n") + "\n\n" + prompt
		}

		userMsg := &genai.Content{
			Role: "user",
			Parts: []*genai.Part{
				{Text: prompt},
			},
		}

		runConfig := runner.WithStateDelta(nil)
		events := cr.adkRunner.Run(ctx, userID, sessionID, userMsg, agent.RunConfig{
			StreamingMode: agent.StreamingModeSSE,
		}, runConfig)

		var responseTextLen int
		for ev, err := range events {
			if ctx.Err() != nil {
				rollbackPendingEdits()
				return
			}
			if err != nil {
				LogError(CatSystem, "runner_event_error", "Error received during agent run loop event streaming", err, map[string]any{
					"session_id": sessionID,
				})
				onError(err)
				return
			}
			if ev != nil {
				// Track response length for HookAgentResponse
				if ev.Content != nil {
					for _, p := range ev.Content.Parts {
						if p != nil && p.Text != "" {
							responseTextLen += len(p.Text)
						}
					}
				}
				onEvent(ev)
			}
		}

		// Fire HookAgentResponse after LLM response is fully received
		cr.deps.HookManager.RunHooks(HookAgentResponse, HookContext{
			ResponseLength: responseTextLen,
			SessionID:      sessionID,
		})

		commitPendingEdits()

		LogInfo(CatSystem, "runner_complete", "Agent execution completed successfully", map[string]any{
			"session_id": sessionID,
		})

		// Trigger Aider-style Git Auto-Commit if repository has staged/unstaged changes
		if hasChanges, err := GitHasChanges(); err == nil && hasChanges {
			if diffStr, err := GitGetStagedDiff(); err == nil && strings.TrimSpace(diffStr) != "" {
				if len(diffStr) > 8000 {
					diffStr = diffStr[:8000]
				}

				gitPrompt := fmt.Sprintf(`You are a professional Git commit assistant. Generate a concise, semantic Git Commit Message based on the following code changes (Git Diff).

Requirements:
1. Must use semantic commit conventions (e.g. feat: ..., fix: ..., chore: ..., refactor: ..., test: ...).
2. Must be within 50 characters.
3. Must return only the commit message itself, without any markdown markers, quotes, paragraphs, or explanatory text.

[Code Changes (Git Diff)]:
%s`, diffStr)

				req := &model.LLMRequest{
					Contents: []*genai.Content{
						{
							Role: "user",
							Parts: []*genai.Part{
								{Text: gitPrompt},
							},
						},
					},
				}

				var commitMsgBuilder strings.Builder
				events := cr.llmModel.GenerateContent(ctx, req, false)
				for resp, err := range events {
					if err == nil && resp != nil && resp.Content != nil && len(resp.Content.Parts) > 0 {
						commitMsgBuilder.WriteString(resp.Content.Parts[0].Text)
					}
				}

				commitMsg := strings.TrimSpace(commitMsgBuilder.String())
				commitMsg = strings.Trim(commitMsg, "\"`'")
				if commitMsg == "" {
					commitMsg = "chore: update files by iroha"
				}

				fullCommitMsg := fmt.Sprintf("[iroha] %s", commitMsg)
				if commitErr := GitCommit(fullCommitMsg); commitErr == nil {
					LogInfo(CatSystem, "git_auto_commit", fmt.Sprintf("Aider-style Git auto-commit completed: %s", fullCommitMsg), map[string]any{
						"session_id": sessionID,
						"msg":        fullCommitMsg,
					})
				} else {
					LogError(CatSystem, "git_auto_commit_failed", "Aider-style Git auto-commit failed", commitErr, map[string]any{
						"session_id": sessionID,
					})
				}
			}
		}

		// Fire HookSessionEnd before signaling completion
		cr.deps.HookManager.RunHooks(HookSessionEnd, HookContext{
			SessionID: sessionID,
		})

		onDone()
	}()
}
