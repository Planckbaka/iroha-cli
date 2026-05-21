package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/session"
)

// SerializedSession represents the full schema serialized to disk for a session.
type SerializedSession struct {
	ID             string           `json:"id"`
	AppName        string           `json:"app_name"`
	UserID         string           `json:"user_id"`
	LastUpdateTime time.Time        `json:"last_update_time"`
	State          map[string]any   `json:"state"`
	Events         []*session.Event `json:"events"`
	CWD            string           `json:"cwd"`
	FirstPrompt    string           `json:"first_prompt"`
}

// SessionMetadata represents the key metadata of a saved session, used for TUI picker.
type SessionMetadata struct {
	ID             string    `json:"id"`
	CWD            string    `json:"cwd"`
	FirstPrompt    string    `json:"first_prompt"`
	LastUpdateTime time.Time `json:"last_update_time"`
}

// PersistentSessionService wraps a session.Service delegate (typically InMemoryService)
// and handles serializing session state and history to/from JSON files.
type PersistentSessionService struct {
	delegate    session.Service
	sessionsDir string
	mu          sync.RWMutex
}

// NewPersistentSessionService creates a new PersistentSessionService.
func NewPersistentSessionService(delegate session.Service, sessionsDir string) *PersistentSessionService {
	_ = os.MkdirAll(sessionsDir, 0755)
	return &PersistentSessionService{
		delegate:    delegate,
		sessionsDir: sessionsDir,
	}
}

// Create delegates to the underlying service and persists the session.
func (s *PersistentSessionService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	resp, err := s.delegate.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.SaveSession(ctx, resp.Session); err != nil {
		// Log error but do not fail the execution
		fmt.Fprintf(os.Stderr, "警告: 无法持久化新建会话: %v\n", err)
	}
	return resp, nil
}

// Get delegates to the underlying service.
func (s *PersistentSessionService) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	return s.delegate.Get(ctx, req)
}

// List delegates to the underlying service.
func (s *PersistentSessionService) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	return s.delegate.List(ctx, req)
}

// Delete delegates to the underlying service and deletes the persisted JSON file.
func (s *PersistentSessionService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	err := s.delegate.Delete(ctx, req)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	filePath := filepath.Join(s.sessionsDir, req.SessionID+".json")
	_ = os.Remove(filePath)
	return nil
}

// AppendEvent delegates to the underlying service and updates the persisted JSON file.
func (s *PersistentSessionService) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	err := s.delegate.AppendEvent(ctx, sess, ev)
	if err != nil {
		return err
	}
	if err := s.SaveSession(ctx, sess); err != nil {
		fmt.Fprintf(os.Stderr, "警告: 无法保存追加事件的会话: %v\n", err)
	}
	return nil
}

// SaveSession serializes the session's current state and events, and writes them to a JSON file.
func (s *PersistentSessionService) SaveSession(ctx context.Context, sess session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Extract state map
	stateMap := make(map[string]any)
	if sess.State() != nil {
		for k, v := range sess.State().All() {
			stateMap[k] = v
		}
	}

	// Extract events
	var events []*session.Event
	if sess.Events() != nil {
		for ev := range sess.Events().All() {
			events = append(events, ev)
		}
	}

	// Determine first prompt
	firstPrompt := getFirstPrompt(events)

	// Get current working directory
	cwd, _ := os.Getwd()

	serialized := SerializedSession{
		ID:             sess.ID(),
		AppName:        sess.AppName(),
		UserID:         sess.UserID(),
		LastUpdateTime: sess.LastUpdateTime(),
		State:          stateMap,
		Events:         events,
		CWD:            cwd,
		FirstPrompt:    firstPrompt,
	}

	data, err := json.MarshalIndent(serialized, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session %s failed: %w", sess.ID(), err)
	}

	filePath := filepath.Join(s.sessionsDir, sess.ID()+".json")
	return os.WriteFile(filePath, data, 0644)
}

// LoadSessions parses all session JSON files and hydates the delegate memory service.
func (s *PersistentSessionService) LoadSessions(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, err := os.ReadDir(s.sessionsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(s.sessionsDir, file.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var serialized SerializedSession
		if err := json.Unmarshal(data, &serialized); err != nil {
			continue
		}

		// Recreate the session in delegate
		res, err := s.delegate.Create(ctx, &session.CreateRequest{
			AppName:   serialized.AppName,
			UserID:    serialized.UserID,
			SessionID: serialized.ID,
			State:     serialized.State,
		})
		if err != nil {
			continue
		}

		// Append events in order
		for _, ev := range serialized.Events {
			_ = s.delegate.AppendEvent(ctx, res.Session, ev)
		}
	}

	return nil
}

// ListSavedSessions returns metadata for all saved sessions, sorted by last update time descending.
func (s *PersistentSessionService) ListSavedSessions() ([]SessionMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	files, err := os.ReadDir(s.sessionsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var metaList []SessionMetadata
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(s.sessionsDir, file.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var serialized SerializedSession
		if err := json.Unmarshal(data, &serialized); err != nil {
			continue
		}

		firstPrompt := serialized.FirstPrompt
		if firstPrompt == "" {
			firstPrompt = getFirstPrompt(serialized.Events)
		}

		metaList = append(metaList, SessionMetadata{
			ID:             serialized.ID,
			CWD:            serialized.CWD,
			FirstPrompt:    firstPrompt,
			LastUpdateTime: serialized.LastUpdateTime,
		})
	}

	// Sort by last update time descending
	sort.Slice(metaList, func(i, j int) bool {
		return metaList[i].LastUpdateTime.After(metaList[j].LastUpdateTime)
	})

	return metaList, nil
}

// ForkSession copies an existing session into a new one and hydrates the delegate.
func (s *PersistentSessionService) ForkSession(ctx context.Context, originalID string, newID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	originalPath := filepath.Join(s.sessionsDir, originalID+".json")
	data, err := os.ReadFile(originalPath)
	if err != nil {
		return fmt.Errorf("read original session file failed: %w", err)
	}

	var serialized SerializedSession
	if err := json.Unmarshal(data, &serialized); err != nil {
		return fmt.Errorf("unmarshal original session failed: %w", err)
	}

	// Update with new session identity
	serialized.ID = newID
	serialized.LastUpdateTime = time.Now()

	// Write cloned file to disk
	clonedPath := filepath.Join(s.sessionsDir, newID+".json")
	clonedData, err := json.MarshalIndent(serialized, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(clonedPath, clonedData, 0644); err != nil {
		return err
	}

	// Hydrate delegate in memory
	res, err := s.delegate.Create(ctx, &session.CreateRequest{
		AppName:   serialized.AppName,
		UserID:    serialized.UserID,
		SessionID: serialized.ID,
		State:     serialized.State,
	})
	if err != nil {
		return err
	}

	for _, ev := range serialized.Events {
		_ = s.delegate.AppendEvent(ctx, res.Session, ev)
	}

	return nil
}

// getFirstPrompt returns the first user message text as the session title.
func getFirstPrompt(events []*session.Event) string {
	for _, ev := range events {
		if ev.Content != nil {
			for _, part := range ev.Content.Parts {
				if part.Text != "" {
					p := strings.TrimSpace(part.Text)
					if p != "" {
						// Clean up status tags if any
						if strings.HasPrefix(p, "<background-results>") || strings.HasPrefix(p, "<scheduled-results>") {
							// Try to skip system injected tags
							lines := strings.Split(p, "\n")
							for _, line := range lines {
								lineTrim := strings.TrimSpace(line)
								if lineTrim != "" && !strings.HasPrefix(lineTrim, "<") && !strings.HasPrefix(lineTrim, "</") {
									return lineTrim
								}
							}
						}
						// Limit title length to 60 characters
						if len(p) > 60 {
							return p[:57] + "..."
						}
						return p
					}
				}
			}
		}
	}
	return "新会话"
}

// GetSessionsDir returns the default directory path for session JSON files.
func GetSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".iroha", "sessions")
	}
	return filepath.Join(home, ".iroha", "sessions")
}

// CleanOldSessions is a helper to clean session files that have not been updated.
func CleanOldSessions(sessionsDir string, maxAge time.Duration) int {
	files, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		info, err := file.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(sessionsDir, file.Name()))
			count++
		}
	}
	return count
}

// Ensure interface matching
var _ session.Service = (*PersistentSessionService)(nil)
