package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/adk/session"
)

func TestPersistentSessionService(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	inMem := session.InMemoryService()
	pService := NewPersistentSessionService(inMem, tmpDir)

	// Test 1: Create Session
	createRes, err := pService.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "sess-1",
		State: map[string]any{
			"key1": "val1",
		},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if createRes.Session.ID() != "sess-1" {
		t.Errorf("expected session ID to be sess-1, got %s", createRes.Session.ID())
	}

	// Verify file is written
	filePath := filepath.Join(tmpDir, "sess-1.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("session file %s does not exist", filePath)
	}

	// Test 2: Append Event
	ev := session.NewEvent("inv-1")
	ev.Author = "user"
	ev.Timestamp = time.Now()

	err = pService.AppendEvent(ctx, createRes.Session, ev)
	if err != nil {
		t.Fatalf("failed to append event: %v", err)
	}

	// Test 3: List Sessions
	metaList, err := pService.ListSavedSessions()
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}

	if len(metaList) != 1 {
		t.Errorf("expected 1 session, got %d", len(metaList))
	} else {
		if metaList[0].ID != "sess-1" {
			t.Errorf("expected ID sess-1, got %s", metaList[0].ID)
		}
	}

	// Test 4: Fork Session
	err = pService.ForkSession(ctx, "sess-1", "sess-2")
	if err != nil {
		t.Fatalf("failed to fork session: %v", err)
	}

	metaList, err = pService.ListSavedSessions()
	if err != nil {
		t.Fatalf("failed to list sessions after fork: %v", err)
	}

	if len(metaList) != 2 {
		t.Errorf("expected 2 sessions after fork, got %d", len(metaList))
	}

	// Test 5: Load Sessions on a new instance
	inMem2 := session.InMemoryService()
	pService2 := NewPersistentSessionService(inMem2, tmpDir)

	err = pService2.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("failed to load sessions: %v", err)
	}

	getRes, err := pService2.Get(ctx, &session.GetRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "sess-2",
	})
	if err != nil {
		t.Fatalf("failed to get forked session from rehydrated service: %v", err)
	}

	if getRes.Session.ID() != "sess-2" {
		t.Errorf("expected loaded session ID to be sess-2, got %s", getRes.Session.ID())
	}
}
