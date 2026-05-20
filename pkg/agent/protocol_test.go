package agent

import (
	"os"
	"testing"
)

func TestProtocolManager_Lifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-protocol-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pm := &ProtocolManager{
		requestsDir: tempDir,
	}

	payload := map[string]any{
		"plan": "Refactor cmd/agent-cli/main.go to move TUI into separate package.",
		"risk": "medium",
	}

	req, err := pm.CreateRequest("plan_approval", "architect", "lead-dev", payload)
	if err != nil {
		t.Fatalf("CreateRequest failed: %v", err)
	}

	if req.RequestID == "" || req.Status != "pending" || req.Sender != "architect" || req.Receiver != "lead-dev" {
		t.Errorf("unexpected request state: %+v", req)
	}

	// Retrieve
	req2, err := pm.GetRequest(req.RequestID)
	if err != nil {
		t.Fatalf("GetRequest failed: %v", err)
	}
	if req2.Payload["risk"] != "medium" {
		t.Errorf("expected payload risk 'medium', got %v", req2.Payload["risk"])
	}

	// Approve
	req3, err := pm.RespondToRequest(req.RequestID, true, "Proceed with caution, keep TUI responsive.")
	if err != nil {
		t.Fatalf("RespondToRequest approval failed: %v", err)
	}

	if req3.Status != "approved" || req3.Comment != "Proceed with caution, keep TUI responsive." {
		t.Errorf("unexpected approved request state: %+v", req3)
	}

	// Try to respond again - should fail
	_, err = pm.RespondToRequest(req.RequestID, false, "Second answer")
	if err == nil {
		t.Error("expected second response attempt to fail, but it succeeded")
	}
}

func TestProtocolManager_Shutdown(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-protocol-shutdown-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	pm := &ProtocolManager{
		requestsDir: tempDir,
	}

	req, err := pm.CreateRequest("shutdown", "lead-dev", "architect", map[string]any{"reason": "task finished"})
	if err != nil {
		t.Fatalf("CreateRequest failed: %v", err)
	}

	req2, err := pm.RespondToRequest(req.RequestID, true, "Ack.")
	if err != nil {
		t.Fatalf("RespondToRequest failed: %v", err)
	}

	if req2.Status != "completed" {
		t.Errorf("expected shutdown request to transition to completed, got: %s", req2.Status)
	}
}
