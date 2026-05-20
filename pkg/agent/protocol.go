package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProtocolRequest represents a structured request-response handshake between agents.
type ProtocolRequest struct {
	RequestID string         `json:"request_id"`
	Type      string         `json:"type"` // "shutdown", "plan_approval"
	Sender    string         `json:"sender"`
	Receiver  string         `json:"receiver"`
	Payload   map[string]any `json:"payload"`
	Status    string         `json:"status"` // "pending", "approved", "rejected", "completed"
	Comment   string         `json:"comment,omitempty"`
	CreatedAt int64          `json:"created_at"`
	UpdatedAt int64          `json:"updated_at"`
}

// ProtocolManager manages durable protocols between teammates.
type ProtocolManager struct {
	mu          sync.RWMutex
	requestsDir string
}

// NewProtocolManager creates a new ProtocolManager.
func NewProtocolManager() *ProtocolManager {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findProjectRoot(wd)
	reqDir := filepath.Join(root, ".team", "requests")
	_ = os.MkdirAll(reqDir, 0755)

	return &ProtocolManager{
		requestsDir: reqDir,
	}
}

// GlobalProtocolManager is the singleton protocol manager.
var GlobalProtocolManager = NewProtocolManager()

// CreateRequest creates a new protocol request, persists it and returns it.
func (pm *ProtocolManager) CreateRequest(reqType, sender, receiver string, payload map[string]any) (*ProtocolRequest, error) {
	if reqType == "" || sender == "" || receiver == "" {
		return nil, fmt.Errorf("type, sender, and receiver are required")
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	reqID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	req := &ProtocolRequest{
		RequestID: reqID,
		Type:      reqType,
		Sender:    sender,
		Receiver:  receiver,
		Payload:   payload,
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}

	if err := pm.saveRequestRaw(req); err != nil {
		return nil, err
	}
	return req, nil
}

// GetRequest retrieves a request by ID.
func (pm *ProtocolManager) GetRequest(requestID string) (*ProtocolRequest, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	path := filepath.Join(pm.requestsDir, requestID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("request %s not found: %w", requestID, err)
	}

	var req ProtocolRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("failed to parse request %s: %w", requestID, err)
	}
	return &req, nil
}

// RespondToRequest updates the request status and saves it.
func (pm *ProtocolManager) RespondToRequest(requestID string, approved bool, comment string) (*ProtocolRequest, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	path := filepath.Join(pm.requestsDir, requestID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("request %s not found: %w", requestID, err)
	}

	var req ProtocolRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("failed to parse request %s: %w", requestID, err)
	}

	if req.Status != "pending" {
		return nil, fmt.Errorf("request %s has already been processed (status: %s)", requestID, req.Status)
	}

	if approved {
		if req.Type == "shutdown" {
			req.Status = "completed"
		} else {
			req.Status = "approved"
		}
	} else {
		req.Status = "rejected"
	}

	req.Comment = comment
	req.UpdatedAt = time.Now().Unix()

	if err := pm.saveRequestRaw(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

func (pm *ProtocolManager) saveRequestRaw(req *ProtocolRequest) error {
	path := filepath.Join(pm.requestsDir, req.RequestID+".json")
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal request %s: %w", req.RequestID, err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write request %s: %w", req.RequestID, err)
	}
	return nil
}
