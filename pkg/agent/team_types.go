package agent

import (
	"context"
	"sync"
	"time"
)

// TeamMessage represents a message sent to a teammate's inbox.
type TeamMessage struct {
	Sender    string         `json:"sender"`
	Content   string         `json:"content"`
	Timestamp float64        `json:"timestamp"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// Teammate represents a specialist agent in the team.
type Teammate struct {
	Name         string    `json:"name"`
	Role         string    `json:"role"`
	Type         string    `json:"type,omitempty"` // "explore", "planner", "reviewer", "executor", "researcher"
	SystemPrompt string    `json:"system_prompt"`
	Status       string    `json:"status"` // "idle", "working", "offline"
	LastActive   time.Time `json:"last_active"`
}

// TeamConfig is the persistent roster configuration.
type TeamConfig struct {
	Teammates []Teammate `json:"teammates"`
}

// TeamManager manages persistent specialist teammates and their mailboxes.
type TeamManager struct {
	mu             sync.RWMutex
	teamDir        string
	teammates      map[string]*Teammate
	activeLoops    map[string]chan struct{}
	ProcessMessage func(teammate *Teammate, msg TeamMessage) (string, error)

	// Process isolation fields
	isolationMode bool                          // true = use child processes, false = goroutines (default)
	ipcBridge     *IPCBridge                    // IPC bridge for process isolation
	watchdogs     map[string]*Watchdog          // teammate name -> watchdog
	binaryPath    string                        // path to the binary for spawning child processes
	cancelFuncs   map[string]context.CancelFunc // teammate name -> cancel function
}
