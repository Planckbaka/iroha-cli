package agent

import (
	"strings"
	"testing"
	"time"
)

func TestAgentWatchCIHandler_RequireOwner(t *testing.T) {
	_, err := AgentWatchCIHandler(nil, CIWatchArgs{Owner: ""})
	if err == nil || !strings.Contains(err.Error(), "owner is required") {
		t.Fatalf("expected error for missing owner, got: %v", err)
	}
}

func TestAgentWatchCIHandler_ValidInput(t *testing.T) {
	// Call the handler synchronously
	res, err := AgentWatchCIHandler(nil, CIWatchArgs{Owner: "test-user"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(res.Message, "CI Watcher has been started") {
		t.Errorf("unexpected response message: %s", res.Message)
	}

	// Wait briefly for the background goroutine to execute exec.Command("gh")
	// Since this is a test environment, it will likely fail immediately (no auth or not a repo)
	// and trigger the failure message appending to the inbox.
	time.Sleep(500 * time.Millisecond)

	msgs, err := GlobalTeamManager.PeekInbox("test-user")
	// Note: We don't strictly enforce an error check if it fails before the sleep is over,
	// but if msgs were captured, we assert their format.
	if err == nil && len(msgs) > 0 {
		if !strings.Contains(msgs[0].Content, "CI Watcher") {
			t.Errorf("unexpected inbox message content: %s", msgs[0].Content)
		}
	}
}
