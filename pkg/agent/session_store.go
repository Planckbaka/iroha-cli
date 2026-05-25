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
	TotalTokens    int       `json:"total_tokens"`
	TotalCost      float64   `json:"total_cost"`
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
		LogError(CatSession, "session_create_failed", "Failed to create session in memory delegate", err, map[string]any{"request": req})
		return nil, err
	}
	LogInfo(CatSession, "session_create", fmt.Sprintf("Session '%s' created for user '%s'", resp.Session.ID(), resp.Session.UserID()), map[string]any{
		"session_id": resp.Session.ID(),
		"app_name":   resp.Session.AppName(),
		"user_id":    resp.Session.UserID(),
	})
	if err := s.SaveSession(ctx, resp.Session); err != nil {
		// Log error but do not fail the execution
		fmt.Fprintf(os.Stderr, "Warning: failed to persist new session: %v\n", err)
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
		LogError(CatSession, "session_delete_failed", "Failed to delete session from memory delegate", err, map[string]any{"session_id": req.SessionID})
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	filePath := filepath.Join(s.sessionsDir, req.SessionID+".json")
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		LogError(CatSession, "session_file_delete_failed", fmt.Sprintf("Failed to delete session file: %s", filePath), err, map[string]any{"session_id": req.SessionID, "path": filePath})
	} else {
		LogInfo(CatSession, "session_delete", fmt.Sprintf("Session '%s' deleted successfully", req.SessionID), map[string]any{
			"session_id": req.SessionID,
			"path":       filePath,
		})
	}
	return nil
}

// AppendEvent delegates to the underlying service and updates the persisted JSON file.
func (s *PersistentSessionService) AppendEvent(ctx context.Context, sess session.Session, ev *session.Event) error {
	err := s.delegate.AppendEvent(ctx, sess, ev)
	if err != nil {
		return err
	}
	if err := s.SaveSession(ctx, sess); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save session with appended event: %v\n", err)
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
		errWrap := fmt.Errorf("marshal session %s failed: %w", sess.ID(), err)
		LogError(CatSession, "session_marshal_failed", "Failed to marshal session JSON", errWrap, map[string]any{"session_id": sess.ID()})
		return errWrap
	}

	filePath := filepath.Join(s.sessionsDir, sess.ID()+".json")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		LogError(CatSession, "session_write_failed", fmt.Sprintf("Failed to write session file to path: %s", filePath), err, map[string]any{"session_id": sess.ID(), "path": filePath})
		return err
	}

	LogInfo(CatSession, "session_save", fmt.Sprintf("Session '%s' saved successfully to disk", sess.ID()), map[string]any{
		"session_id":  sess.ID(),
		"path":        filePath,
		"event_count": len(events),
	})
	return nil
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
		LogError(CatSession, "load_sessions_failed", "Failed to read sessions directory", err, map[string]any{"directory": s.sessionsDir})
		return err
	}

	loadedCount := 0
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(s.sessionsDir, file.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			LogError(CatSession, "session_read_failed", fmt.Sprintf("Failed to read session file: %s", filePath), err, map[string]any{"path": filePath})
			continue
		}

		var serialized SerializedSession
		if err := json.Unmarshal(data, &serialized); err != nil {
			LogError(CatSession, "session_unmarshal_failed", fmt.Sprintf("Failed to unmarshal session file: %s", filePath), err, map[string]any{"path": filePath})
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
			LogError(CatSession, "session_recreate_failed", fmt.Sprintf("Failed to recreate session '%s' in memory delegate during load", serialized.ID), err, map[string]any{"session_id": serialized.ID})
			continue
		}

		// Append events in order
		for _, ev := range serialized.Events {
			_ = s.delegate.AppendEvent(ctx, res.Session, ev)
		}
		loadedCount++
	}

	LogInfo(CatSession, "sessions_load_completed", fmt.Sprintf("Successfully loaded %d sessions from disk", loadedCount), map[string]any{
		"loaded_count": loadedCount,
		"directory":    s.sessionsDir,
	})
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

		totalTextLen := 0
		for _, ev := range serialized.Events {
			if ev == nil {
				continue
			}
			if ev.Content != nil {
				for _, part := range ev.Content.Parts {
					totalTextLen += len(part.Text)
				}
			}
			if ev.LLMResponse.Content != nil {
				for _, part := range ev.LLMResponse.Content.Parts {
					totalTextLen += len(part.Text)
				}
			}
		}
		totalTokens := totalTextLen / 4
		totalCost := float64(totalTokens) * 2.0 / 1000000.0 // Baseline estimate: $2.00 per million tokens

		metaList = append(metaList, SessionMetadata{
			ID:             serialized.ID,
			CWD:            serialized.CWD,
			FirstPrompt:    firstPrompt,
			LastUpdateTime: serialized.LastUpdateTime,
			TotalTokens:    totalTokens,
			TotalCost:      totalCost,
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
		errWrap := fmt.Errorf("read original session file failed: %w", err)
		LogError(CatSession, "session_fork_failed", fmt.Sprintf("Failed to read original session file for fork: %s", originalPath), errWrap, map[string]any{"original_id": originalID, "new_id": newID})
		return errWrap
	}

	var serialized SerializedSession
	if err := json.Unmarshal(data, &serialized); err != nil {
		errWrap := fmt.Errorf("unmarshal original session failed: %w", err)
		LogError(CatSession, "session_fork_failed", "Failed to unmarshal original session during fork", errWrap, map[string]any{"original_id": originalID, "new_id": newID})
		return errWrap
	}

	// Update with new session identity
	serialized.ID = newID
	serialized.LastUpdateTime = time.Now()

	// Write cloned file to disk
	clonedPath := filepath.Join(s.sessionsDir, newID+".json")
	clonedData, err := json.MarshalIndent(serialized, "", "  ")
	if err != nil {
		LogError(CatSession, "session_fork_failed", "Failed to marshal cloned session during fork", err, map[string]any{"original_id": originalID, "new_id": newID})
		return err
	}
	if err := os.WriteFile(clonedPath, clonedData, 0644); err != nil {
		LogError(CatSession, "session_fork_failed", fmt.Sprintf("Failed to write cloned session file: %s", clonedPath), err, map[string]any{"original_id": originalID, "new_id": newID, "cloned_path": clonedPath})
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
		LogError(CatSession, "session_fork_failed", "Failed to create cloned session in memory delegate", err, map[string]any{"original_id": originalID, "new_id": newID})
		return err
	}

	for _, ev := range serialized.Events {
		_ = s.delegate.AppendEvent(ctx, res.Session, ev)
	}

	LogAudit(CatSession, "session_fork", fmt.Sprintf("Successfully forked session '%s' into '%s'", originalID, newID), map[string]any{
		"original_id": originalID,
		"new_id":      newID,
	})
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
	return "New Session"
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
