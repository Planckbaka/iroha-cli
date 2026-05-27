package agent

import (
	"sync"
	"time"
)

// ConfirmationBridge synchronizes the background agent runner and the foreground TUI
type ConfirmationBridge struct {
	PromptChan   chan string // Agent sends confirmation prompts here
	ResponseChan chan string // TUI sends user responses (y/n/always) here
	CancelChan   chan struct{}
	cancelMu     sync.Mutex
}

var Bridge = &ConfirmationBridge{
	PromptChan:   make(chan string, 1),
	ResponseChan: make(chan string, 1),
	CancelChan:   make(chan struct{}),
}

func (b *ConfirmationBridge) Reset() {
	b.cancelMu.Lock()
	defer b.cancelMu.Unlock()

	// Drain stale prompts
	for len(b.PromptChan) > 0 {
		<-b.PromptChan
	}
	// Drain stale responses
	for len(b.ResponseChan) > 0 {
		<-b.ResponseChan
	}
	// Reset CancelChan
	b.CancelChan = make(chan struct{})
}

func (b *ConfirmationBridge) CancelChanRead() <-chan struct{} {
	b.cancelMu.Lock()
	ch := b.CancelChan
	b.cancelMu.Unlock()
	return ch
}

func (b *ConfirmationBridge) Cancel() {
	b.cancelMu.Lock()
	close(b.CancelChan)
	b.cancelMu.Unlock()
}

// ToolStatus represents the real-time execution state of a tool
type ToolStatus struct {
	Name        string
	Args        any
	Running     bool
	Success     bool
	Error       error
	Duration    time.Duration
	StreamLines []string // incremental output lines (only for shell_run line-by-line streaming)
}

// ToolStatusBridge pipes tool status changes from the background runner to the foreground TUI
type ToolStatusBridge struct {
	StatusChan chan ToolStatus
	mu         sync.Mutex
	queue      []ToolStatus
	active     bool
}

var ToolBridge = &ToolStatusBridge{
	StatusChan: make(chan ToolStatus, 100),
}

func (tb *ToolStatusBridge) Send(status ToolStatus) {
	tb.mu.Lock()
	tb.queue = append(tb.queue, status)
	if !tb.active {
		tb.active = true
		go tb.drain()
	}
	tb.mu.Unlock()
}

func (tb *ToolStatusBridge) drain() {
	for {
		tb.mu.Lock()
		if len(tb.queue) == 0 {
			tb.active = false
			tb.mu.Unlock()
			return
		}
		status := tb.queue[0]
		tb.queue = tb.queue[1:]
		tb.mu.Unlock()

		// Blocking send in background worker ensures 100% delivery and order preservation
		tb.StatusChan <- status
	}
}
