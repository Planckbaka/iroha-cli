package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// NewTeamManager creates a new TeamManager.
func NewTeamManager() *TeamManager {
	return &TeamManager{
		teamDir:     ResolveTeamDir(),
		teammates:   make(map[string]*Teammate),
		activeLoops: make(map[string]chan struct{}),
		watchdogs:   make(map[string]*Watchdog),
		cancelFuncs: make(map[string]context.CancelFunc),
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
			tm.teammates = make(map[string]*Teammate)
		} else {
			return fmt.Errorf("failed to read team config: %w", err)
		}
	} else {
		var cfg TeamConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("failed to parse team config: %w", err)
		}

		tm.teammates = make(map[string]*Teammate)
		for i := range cfg.Teammates {
			t := cfg.Teammates[i]
			tm.teammates[t.Name] = &t
		}
	}

	_ = tm.loadYAMLAgents()
	return nil
}

// loadYAMLAgents dynamically scans and registers YAML-declared subagents from (.iroha/agents/ & .claude/agents/)
func (tm *TeamManager) loadYAMLAgents() error {
	var searchDirs []string

	// Local project directories
	if cwd, err := os.Getwd(); err == nil {
		searchDirs = append(searchDirs,
			filepath.Join(cwd, ".iroha", "agents"),
			filepath.Join(cwd, ".claude", "agents"),
		)
	}

	// User home directories
	if home, err := os.UserHomeDir(); err == nil {
		searchDirs = append(searchDirs,
			filepath.Join(home, ".iroha", "agents"),
			filepath.Join(home, ".claude", "agents"),
		)
	}

	type yamlMeta struct {
		Name        string `yaml:"name"`
		Role        string `yaml:"role,omitempty"`
		Description string `yaml:"description,omitempty"`
		Type        string `yaml:"type,omitempty"`
	}

	for _, dir := range searchDirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue // skip directory if doesn't exist
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(file.Name()))
			if ext != ".yaml" && ext != ".yml" {
				continue
			}

			filePath := filepath.Join(dir, file.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			// Parse frontmatter
			content := string(data)
			var frontmatter string
			var systemPrompt string

			if strings.HasPrefix(content, "---") {
				// Format:
				// ---
				// yaml metadata
				// ---
				// system prompt
				parts := strings.SplitN(content, "---", 3)
				if len(parts) >= 3 {
					frontmatter = parts[1]
					systemPrompt = strings.TrimSpace(parts[2])
				} else {
					frontmatter = content
				}
			} else {
				frontmatter = content
			}

			var meta yamlMeta
			if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
				continue // bad YAML metadata, skip
			}

			if meta.Name == "" {
				continue
			}

			role := meta.Role
			if role == "" {
				role = meta.Description
			}
			if role == "" {
				role = "Specialist Agent"
			}

			tm.teammates[meta.Name] = &Teammate{
				Name:         meta.Name,
				Role:         role,
				Type:         meta.Type,
				SystemPrompt: systemPrompt,
				Status:       "idle",
				LastActive:   time.Now(),
			}
		}
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
func (tm *TeamManager) RegisterTeammate(name, role, systemPrompt, agentType string) (*Teammate, error) {
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
	t.Type = agentType
	t.SystemPrompt = systemPrompt
	t.Status = "idle"
	t.LastActive = time.Now()

	if err := tm.SaveConfig(); err != nil {
		LogError(CatSubagent, "subagent_register_failed", fmt.Sprintf("Failed to save team config during teammate '%s' registration", name), err, map[string]any{"name": name})
		return nil, err
	}

	LogInfo(CatSubagent, "subagent_registered", fmt.Sprintf("Specialist teammate '%s' registered (Role: %s)", name, role), map[string]any{
		"name": name,
		"role": role,
	})

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

// StartTeammateLoop spawns a background goroutine for a teammate to process its inbox.
// When process isolation is enabled, it spawns a child process instead.
func (tm *TeamManager) StartTeammateLoop(name string) error {
	tm.mu.RLock()
	isolated := tm.isolationMode
	tm.mu.RUnlock()

	if isolated {
		return tm.StartTeammateProcess(context.Background(), name)
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, active := tm.activeLoops[name]; active {
		return nil // already running
	}

	stopChan := make(chan struct{})
	tm.activeLoops[name] = stopChan

	LogInfo(CatSubagent, "subagent_loop_started", fmt.Sprintf("Background message processing loop started for teammate '%s'", name), map[string]any{
		"name": name,
	})

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
					startTime := time.Now()
					var response string
					var procErr error
					if tm.ProcessMessage != nil {
						response, procErr = tm.ProcessMessage(t, msg)
					} else {
						// Fallback: simple echo or log if not overridden
						response = fmt.Sprintf("Teammate '%s' received: %s", t.Name, msg.Content)
					}
					durationMS := time.Since(startTime).Milliseconds()

					if procErr != nil {
						LogError(CatSubagent, "subagent_message_failed", fmt.Sprintf("Teammate '%s' failed to process message from '%s'", t.Name, msg.Sender), procErr, map[string]any{
							"sender":      msg.Sender,
							"recipient":   t.Name,
							"content":     msg.Content,
							"duration_ms": durationMS,
						})
					} else {
						GlobalLogger.Log(LevelInfo, CatSubagent, "subagent_message_processed", fmt.Sprintf("Teammate '%s' successfully processed message from '%s' in %dms", t.Name, msg.Sender, durationMS), durationMS, map[string]any{
							"sender":      msg.Sender,
							"recipient":   t.Name,
							"duration_ms": durationMS,
							"response":    response,
						})
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

// StopTeammateLoop stops a teammate's background loop or child process.
func (tm *TeamManager) StopTeammateLoop(name string) {
	// Handle process-isolated teammates
	tm.mu.RLock()
	isolated := tm.isolationMode
	tm.mu.RUnlock()

	if isolated {
		tm.StopTeammateProcess(name)
		return
	}

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

		LogInfo(CatSubagent, "subagent_loop_stopped", fmt.Sprintf("Background loop stopped for teammate '%s'", name), map[string]any{
			"name": name,
		})
	}
}

// EnableProcessIsolation switches the team manager to spawn child processes instead of goroutines.
func (tm *TeamManager) EnableProcessIsolation(binaryPath string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.isolationMode = true
	tm.binaryPath = binaryPath

	// Create socket directory
	socketDir := filepath.Join(tm.teamDir, "sockets")
	_ = os.MkdirAll(socketDir, 0755)

	tm.ipcBridge = NewIPCBridge(socketDir)

	if err := tm.ipcBridge.Start(); err != nil {
		return fmt.Errorf("failed to start IPC bridge: %w", err)
	}

	// Set up message handler for messages coming from child processes
	tm.ipcBridge.SetOnMessage(func(msg IPCMessage) {
		tm.handleIPCMessage(msg)
	})

	LogInfo(CatSubagent, "isolation_enabled", "Process isolation enabled for team", map[string]any{
		"binary": binaryPath,
	})

	return nil
}

// handleIPCMessage processes an incoming IPC message from a child process.
func (tm *TeamManager) handleIPCMessage(msg IPCMessage) {
	switch msg.Type {
	case "message", "task_complete":
		// Child completed processing — forward result to the original sender
		var teamMsg TeamMessage
		if err := json.Unmarshal(msg.Payload, &teamMsg); err == nil && teamMsg.Sender != "" {
			_ = tm.AppendToInbox(teamMsg.Sender, teamMsg)
		}
	case "heartbeat":
		// Heartbeat from child — update last active
		tm.mu.Lock()
		if t, ok := tm.teammates[msg.From]; ok {
			t.LastActive = time.Now()
			t.Status = "working"
			_ = tm.SaveConfig()
		}
		tm.mu.Unlock()

	case "shutdown":
		LogInfo(CatSubagent, "ipc_shutdown", fmt.Sprintf("Teammate '%s' sent shutdown", msg.From), map[string]any{
			"teammate": msg.From,
		})
	}
}

// StartTeammateProcess spawns a teammate as a child process with IPC.
func (tm *TeamManager) StartTeammateProcess(ctx context.Context, name string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, active := tm.activeLoops[name]; active {
		return nil // already running
	}

	if tm.binaryPath == "" {
		return fmt.Errorf("binary path not set; call EnableProcessIsolation first")
	}

	wd := NewWatchdog(name, 3, 60*time.Second)
	wd.loadDeadLetters()

	// Restore checkpoint if available
	cp, err := wd.Recover()
	if err != nil {
		LogWarn(CatSubagent, "checkpoint_restore_failed", fmt.Sprintf("Failed to restore checkpoint for '%s'", name), map[string]any{"error": err.Error()})
	}
	if cp != nil {
		LogInfo(CatSubagent, "checkpoint_restored", fmt.Sprintf("Restored checkpoint for '%s'", name), map[string]any{
			"teammate":  name,
			"saved_at":  cp.SavedAt,
			"processed": cp.Processed,
		})
	}

	// Build child process args
	args := []string{
		"--teammate", name,
		"--socket", tm.ipcBridge.socketPath("parent"),
	}

	ctx, cancel := context.WithCancel(ctx)

	stopChan := make(chan struct{})
	tm.activeLoops[name] = stopChan
	tm.watchdogs[name] = wd
	tm.cancelFuncs[name] = cancel

	LogInfo(CatSubagent, "process_spawning", fmt.Sprintf("Spawning teammate '%s' as child process", name), map[string]any{
		"teammate": name,
		"binary":   tm.binaryPath,
	})

	// Spawn and monitor in a background goroutine
	go func() {
		defer cancel()

		if err := wd.Start(ctx, tm.binaryPath, args); err != nil {
			LogError(CatSubagent, "process_spawn_failed", fmt.Sprintf("Failed to spawn teammate '%s'", name), err, map[string]any{"teammate": name})
			return
		}

		// Mark as working
		tm.mu.Lock()
		if t, ok := tm.teammates[name]; ok {
			t.Status = "working"
			t.LastActive = time.Now()
			_ = tm.SaveConfig()
		}
		tm.mu.Unlock()

		// Monitor blocks until context cancelled or crash budget exceeded
		if err := wd.Monitor(ctx); err != nil {
			LogError(CatSubagent, "watchdog_exceeded", fmt.Sprintf("Watchdog terminated for '%s'", name), err, map[string]any{"teammate": name})
		}

		// Mark as offline
		tm.mu.Lock()
		if t, ok := tm.teammates[name]; ok {
			t.Status = "offline"
			t.LastActive = time.Now()
			_ = tm.SaveConfig()
		}
		tm.mu.Unlock()
	}()

	// Start heartbeat checker
	go tm.heartbeatChecker(ctx, name)

	return nil
}

// heartbeatChecker monitors heartbeat from a child process teammate.
func (tm *TeamManager) heartbeatChecker(ctx context.Context, name string) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	lastActive := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tm.mu.RLock()
			t, ok := tm.teammates[name]
			tm.mu.RUnlock()

			if !ok {
				return
			}

			// If last active is stale and process is running, it may be hung
			if t.LastActive.After(lastActive) {
				lastActive = t.LastActive
			} else if time.Since(lastActive) > 45*time.Second {
				LogWarn(CatSubagent, "heartbeat_stale", fmt.Sprintf("Teammate '%s' heartbeat stale", name), map[string]any{
					"teammate":    name,
					"last_active": t.LastActive,
				})
			}
		}
	}
}

// StopTeammateProcess stops a teammate's child process.
func (tm *TeamManager) StopTeammateProcess(name string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if cancel, ok := tm.cancelFuncs[name]; ok {
		cancel()
		delete(tm.cancelFuncs, name)
	}

	if wd, ok := tm.watchdogs[name]; ok {
		wd.Stop()
		delete(tm.watchdogs, name)
	}

	delete(tm.activeLoops, name)

	if t, ok := tm.teammates[name]; ok {
		t.Status = "offline"
		t.LastActive = time.Now()
		_ = tm.SaveConfig()
	}

	LogInfo(CatSubagent, "process_stopped", fmt.Sprintf("Child process stopped for teammate '%s'", name), map[string]any{
		"name": name,
	})
}

// ResolveTeammateSocketDir returns the socket directory for IPC.
func (tm *TeamManager) ResolveTeammateSocketDir() string {
	return filepath.Join(tm.teamDir, "sockets")
}

// RunTeammateMode runs the current process as a teammate child, connecting to the parent via IPC.
// This is called when the binary is launched with --teammate flag.
func RunTeammateMode(ctx context.Context, teammateName, socketPath string, processMessage func(*Teammate, TeamMessage) (string, error)) error {
	socketDir := filepath.Dir(socketPath)
	bridge := NewIPCBridge(socketDir)

	if err := bridge.Connect(teammateName); err != nil {
		return fmt.Errorf("teammate connect failed: %w", err)
	}
	defer bridge.Close()

	// Send initial heartbeat
	hbPayload, _ := json.Marshal(map[string]string{"status": "ready"})
	_ = bridge.SendToParent(IPCMessage{
		Type:    "heartbeat",
		From:    teammateName,
		To:      "parent",
		Payload: hbPayload,
		ID:      fmt.Sprintf("hb-%d", time.Now().UnixNano()),
	})

	// Heartbeat ticker
	hbTicker := time.NewTicker(10 * time.Second)
	defer hbTicker.Stop()

	go func() {
		for {
			select {
			case <-hbTicker.C:
				payload, _ := json.Marshal(map[string]string{"status": "alive"})
				_ = bridge.SendToParent(IPCMessage{
					Type:    "heartbeat",
					From:    teammateName,
					To:      "parent",
					Payload: payload,
					ID:      fmt.Sprintf("hb-%d", time.Now().UnixNano()),
				})
			case <-ctx.Done():
				return
			}
		}
	}()

	// Process incoming messages
	msgCh := bridge.Receive()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}

			switch msg.Type {
			case "shutdown":
				return nil

			case "message", "task_assign":
				// Decode the team message from payload
				var teamMsg TeamMessage
				if err := json.Unmarshal(msg.Payload, &teamMsg); err != nil {
					LogError(CatSubagent, "teammate_decode_failed", "Failed to decode task message", err, map[string]any{
						"msg_id": msg.ID,
					})
					continue
				}

				// Create a teammate object for processing
				t := &Teammate{
					Name:   teammateName,
					Status: "working",
				}

				var response string
				var procErr error
				if processMessage != nil {
					response, procErr = processMessage(t, teamMsg)
				} else {
					response = fmt.Sprintf("Teammate '%s' received: %s", teammateName, teamMsg.Content)
				}

				if procErr != nil {
					LogError(CatSubagent, "teammate_process_failed", fmt.Sprintf("Teammate '%s' failed to process message", teammateName), procErr, map[string]any{
						"msg_id": msg.ID,
						"sender": teamMsg.Sender,
					})
				}

				// Send result back to parent
				if response != "" {
					replyPayload, _ := json.Marshal(TeamMessage{
						Sender:    teammateName,
						Content:   response,
						Timestamp: float64(time.Now().Unix()),
					})
					_ = bridge.SendToParent(IPCMessage{
						Type:    "task_complete",
						From:    teammateName,
						To:      "parent",
						Payload: replyPayload,
						ID:      fmt.Sprintf("reply-%d", time.Now().UnixNano()),
					})
				}
			}
		}
	}
}
