package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// AgentState represents the execution mode of an autonomous specialist.
type AgentState string

const (
	StateWork AgentState = "WORK"
	StateIdle AgentState = "IDLE"
)

// AutonomousManager coordinates task auto-polling and state transitions.
type AutonomousManager struct {
	mu         sync.RWMutex
	state      AgentState
	activePoll bool
	stopChan   chan struct{}
}

// GlobalAutonomyManager is the singleton autonomy manager.
var GlobalAutonomyManager = &AutonomousManager{
	state: StateIdle,
}

// GetState retrieves the thread-safe state of the agent.
func (am *AutonomousManager) GetState() AgentState {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.state
}

// SetState updates the thread-safe state of the agent.
func (am *AutonomousManager) SetState(state AgentState) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.state = state
}

// AutoClaimTasks queries the task graph for pending unblocked tasks matching role keywords.
func (am *AutonomousManager) AutoClaimTasks(teammateName string, keywords []string) ([]string, error) {
	tasks, err := GlobalTaskManager.ListTasks()
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks for auto-claim: %w", err)
	}

	var claimed []string
	for _, t := range tasks {
		if t.Status != "pending" {
			continue
		}

		// Verify task is unblocked (all dependency tasks must be completed or deleted)
		unblocked := true
		for _, depID := range t.BlockedBy {
			depTask, err := GlobalTaskManager.GetTask(depID)
			if err == nil && depTask.Status != "completed" && depTask.Status != "deleted" {
				unblocked = false
				break
			}
		}

		if !unblocked {
			continue
		}

		// Match subject keywords (case-insensitive)
		match := false
		subjectLower := strings.ToLower(t.Subject)
		for _, kw := range keywords {
			if strings.Contains(subjectLower, strings.ToLower(kw)) {
				match = true
				break
			}
		}

		if match {
			t.Status = "in_progress"
			t.Owner = teammateName
			if err := GlobalTaskManager.SaveTask(t); err == nil {
				claimed = append(claimed, t.ID)
			}
		}
	}

	return claimed, nil
}

// StartAutoPolling boots a background loop scanning tasks periodically while agent is IDLE.
func (am *AutonomousManager) StartAutoPolling(teammateName string, keywords []string, interval time.Duration) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if am.activePoll {
		return
	}
	am.activePoll = true
	am.stopChan = make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-am.stopChan:
				return
			case <-ticker.C:
				if am.GetState() == StateIdle {
					_, _ = am.AutoClaimTasks(teammateName, keywords)
				}
			}
		}
	}()
}

// StopAutoPolling halts the task scanning background routine.
func (am *AutonomousManager) StopAutoPolling() {
	am.mu.Lock()
	defer am.mu.Unlock()

	if !am.activePoll {
		return
	}
	close(am.stopChan)
	am.activePoll = false
}

// GlobalMessageCount represents the current history message count. It is monitored to trigger prompt re-injection.
var GlobalMessageCount = 10

// GetIdentityTagBlock returns the identity block to be injected back into instructions.
func GetIdentityTagBlock() string {
	return `<identity>
Name: iroha
Role: Cybernetic Software Engineering Assistant
	System Prompt: You are a professional software engineering assistant named iroha. Your brand and persona are inspired by a warm, enthusiastic, and energetic science-loving girl from a sci-fi anime. You address the user as "Developer". Your language is measured, calm, and precise, demonstrating the sharp logical thinking and tech-savvy nature of a cybernetic prosthetics engineer. You can help the Developer read files, write files, run tests and commands in the current workspace, and search code. For sensitive operations like writing files and running Shell commands, you must call the appropriate tools, and the framework will request Developer confirmation. Please answer the Developer's questions with keen insight, clear organization, and beautiful Markdown formatting.
</identity>

`
}
