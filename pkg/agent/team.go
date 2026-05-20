package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
}

// NewTeamManager creates a new TeamManager.
func NewTeamManager() *TeamManager {
	return &TeamManager{
		teamDir:     ResolveTeamDir(),
		teammates:   make(map[string]*Teammate),
		activeLoops: make(map[string]chan struct{}),
	}
}

// GlobalTeamManager is the singleton team manager.
var GlobalTeamManager = NewTeamManager()

// ResolveTeamDir locates the persistent directory for teams.
func ResolveTeamDir() string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findProjectRoot(wd)
	teamDir := filepath.Join(root, ".team")
	_ = os.MkdirAll(teamDir, 0755)
	_ = os.MkdirAll(filepath.Join(teamDir, "inbox"), 0755)
	return teamDir
}

// LoadConfig reads the roster from disk.
func (tm *TeamManager) LoadConfig() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	configPath := filepath.Join(tm.teamDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read team config: %w", err)
	}

	var cfg TeamConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse team config: %w", err)
	}

	tm.teammates = make(map[string]*Teammate)
	for i := range cfg.Teammates {
		t := cfg.Teammates[i]
		tm.teammates[t.Name] = &t
	}
	return nil
}

// SaveConfig writes the roster to disk.
func (tm *TeamManager) SaveConfig() error {
	configPath := filepath.Join(tm.teamDir, "config.json")
	var cfg TeamConfig
	cfg.Teammates = make([]Teammate, 0, len(tm.teammates))
	for _, t := range tm.teammates {
		cfg.Teammates = append(cfg.Teammates, *t)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal team config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write team config: %w", err)
	}
	return nil
}

// RegisterTeammate creates or updates a specialist and saves it.
func (tm *TeamManager) RegisterTeammate(name, role, systemPrompt string) (*Teammate, error) {
	if name == "" || role == "" {
		return nil, fmt.Errorf("name and role are required")
	}

	if err := tm.LoadConfig(); err != nil {
		return nil, err
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	t, ok := tm.teammates[name]
	if !ok {
		t = &Teammate{Name: name}
		tm.teammates[name] = t
	}
	t.Role = role
	t.SystemPrompt = systemPrompt
	t.Status = "idle"
	t.LastActive = time.Now()

	if err := tm.SaveConfig(); err != nil {
		return nil, err
	}

	return t, nil
}

// GetTeammate retrieves a teammate by name.
func (tm *TeamManager) GetTeammate(name string) (*Teammate, error) {
	if err := tm.LoadConfig(); err != nil {
		return nil, err
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	t, ok := tm.teammates[name]
	if !ok {
		return nil, fmt.Errorf("teammate '%s' not found", name)
	}
	return t, nil
}

// ListTeammates lists all registered teammates.
func (tm *TeamManager) ListTeammates() ([]Teammate, error) {
	if err := tm.LoadConfig(); err != nil {
		return nil, err
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	list := make([]Teammate, 0, len(tm.teammates))
	for _, t := range tm.teammates {
		list = append(list, *t)
	}
	return list, nil
}

// AppendToInbox appends a message to the teammate's inbox JSONL file.
func (tm *TeamManager) AppendToInbox(name string, msg TeamMessage) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	inboxPath := filepath.Join(tm.teamDir, "inbox", name+".jsonl")
	f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open inbox for %s: %w", name, err)
	}
	defer f.Close()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal inbox message: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write inbox message: %w", err)
	}
	return nil
}

// ReadAndClearInbox reads all messages from an inbox and truncates the file.
func (tm *TeamManager) ReadAndClearInbox(name string) ([]TeamMessage, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	inboxPath := filepath.Join(tm.teamDir, "inbox", name+".jsonl")
	data, err := os.ReadFile(inboxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read inbox for %s: %w", name, err)
	}

	// Truncate/Clear the file
	if err := os.WriteFile(inboxPath, nil, 0644); err != nil {
		return nil, fmt.Errorf("failed to clear inbox for %s: %w", name, err)
	}

	var messages []TeamMessage
	lines := splitJSONLines(data)
	for _, line := range lines {
		var msg TeamMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

// PeekInbox reads all messages from an inbox without clearing it.
func (tm *TeamManager) PeekInbox(name string) ([]TeamMessage, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	inboxPath := filepath.Join(tm.teamDir, "inbox", name+".jsonl")
	data, err := os.ReadFile(inboxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to peek inbox for %s: %w", name, err)
	}

	var messages []TeamMessage
	lines := splitJSONLines(data)
	for _, line := range lines {
		var msg TeamMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

// StartTeammateLoop spawns a background goroutine for a teammate to process its inbox.
func (tm *TeamManager) StartTeammateLoop(name string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, active := tm.activeLoops[name]; active {
		return nil // already running
	}

	stopChan := make(chan struct{})
	tm.activeLoops[name] = stopChan

	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				t, err := tm.GetTeammate(name)
				if err != nil {
					continue
				}

				messages, err := tm.ReadAndClearInbox(name)
				if err != nil || len(messages) == 0 {
					continue
				}

				// Mark as working
				tm.mu.Lock()
				t.Status = "working"
				t.LastActive = time.Now()
				_ = tm.SaveConfig()
				tm.mu.Unlock()

				for _, msg := range messages {
					var response string
					var procErr error
					if tm.ProcessMessage != nil {
						response, procErr = tm.ProcessMessage(t, msg)
					} else {
						// Fallback: simple echo or log if not overridden
						response = fmt.Sprintf("Teammate '%s' received: %s", t.Name, msg.Content)
					}

					if procErr == nil && response != "" {
						// Send reply back to the sender
						reply := TeamMessage{
							Sender:    t.Name,
							Content:   response,
							Timestamp: float64(time.Now().Unix()),
						}
						_ = tm.AppendToInbox(msg.Sender, reply)
					}
				}

				// Mark as idle
				tm.mu.Lock()
				t.Status = "idle"
				t.LastActive = time.Now()
				_ = tm.SaveConfig()
				tm.mu.Unlock()
			}
		}
	}()

	return nil
}

// StopTeammateLoop stops a teammate's background loop.
func (tm *TeamManager) StopTeammateLoop(name string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if stopChan, active := tm.activeLoops[name]; active {
		close(stopChan)
		delete(tm.activeLoops, name)

		if t, ok := tm.teammates[name]; ok {
			t.Status = "offline"
			t.LastActive = time.Now()
			_ = tm.SaveConfig()
		}
	}
}

// Broadcast sends a message to all registered teammates.
func (tm *TeamManager) Broadcast(sender, content string) error {
	teammates, err := tm.ListTeammates()
	if err != nil {
		return err
	}

	for _, t := range teammates {
		if t.Name == sender {
			continue
		}
		msg := TeamMessage{
			Sender:    sender,
			Content:   content,
			Timestamp: float64(time.Now().Unix()),
		}
		_ = tm.AppendToInbox(t.Name, msg)
	}
	return nil
}

// Helper: split JSONL data into lines
func splitJSONLines(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := string(data[start:i])
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		line := string(data[start:])
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
