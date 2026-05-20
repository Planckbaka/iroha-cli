package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTeamManager_RegisterAndLoad(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-team-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tm := &TeamManager{
		teamDir:   tempDir,
		teammates: make(map[string]*Teammate),
	}
	_ = os.MkdirAll(filepath.Join(tempDir, "inbox"), 0755)

	tmate, err := tm.RegisterTeammate("architect", "Software Architect", "Define codebase structures.")
	if err != nil {
		t.Fatalf("RegisterTeammate failed: %v", err)
	}

	if tmate.Name != "architect" || tmate.Role != "Software Architect" || tmate.Status != "idle" {
		t.Errorf("unexpected teammate attributes: %+v", tmate)
	}

	// Reload from disk
	tm2 := &TeamManager{
		teamDir:   tempDir,
		teammates: make(map[string]*Teammate),
	}
	if err := tm2.LoadConfig(); err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	tmate2, err := tm2.GetTeammate("architect")
	if err != nil {
		t.Fatalf("GetTeammate failed: %v", err)
	}

	if tmate2.Role != "Software Architect" || tmate2.SystemPrompt != "Define codebase structures." {
		t.Errorf("reloaded teammate has unexpected attributes: %+v", tmate2)
	}
}

func TestTeamManager_MailboxOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-team-mailbox-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tm := &TeamManager{
		teamDir:   tempDir,
		teammates: make(map[string]*Teammate),
	}
	_ = os.MkdirAll(filepath.Join(tempDir, "inbox"), 0755)

	msg1 := TeamMessage{
		Sender:    "tester",
		Content:   "Please test this function",
		Timestamp: 1000.0,
	}

	if err := tm.AppendToInbox("architect", msg1); err != nil {
		t.Fatalf("AppendToInbox failed: %v", err)
	}

	msg2 := TeamMessage{
		Sender:    "executor",
		Content:   "Code is done",
		Timestamp: 1001.0,
	}

	if err := tm.AppendToInbox("architect", msg2); err != nil {
		t.Fatalf("AppendToInbox failed: %v", err)
	}

	// Read and clear
	msgs, err := tm.ReadAndClearInbox("architect")
	if err != nil {
		t.Fatalf("ReadAndClearInbox failed: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	if msgs[0].Sender != "tester" || msgs[1].Sender != "executor" {
		t.Errorf("messages in unexpected order: %+v", msgs)
	}

	// Inbox should be empty now
	msgs2, err := tm.ReadAndClearInbox("architect")
	if err != nil {
		t.Fatalf("ReadAndClearInbox 2 failed: %v", err)
	}
	if len(msgs2) != 0 {
		t.Errorf("expected inbox to be cleared, got %d messages", len(msgs2))
	}
}

func TestTeamManager_BackgroundWorkerLoop(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-team-loop-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tm := &TeamManager{
		teamDir:     tempDir,
		teammates:   make(map[string]*Teammate),
		activeLoops: make(map[string]chan struct{}),
	}
	_ = os.MkdirAll(filepath.Join(tempDir, "inbox"), 0755)

	_, _ = tm.RegisterTeammate("worker-bob", "Developer", "Write code.")

	processed := make(chan TeamMessage, 1)
	tm.ProcessMessage = func(teammate *Teammate, msg TeamMessage) (string, error) {
		processed <- msg
		return "Done processing: " + msg.Content, nil
	}

	if err := tm.StartTeammateLoop("worker-bob"); err != nil {
		t.Fatalf("StartTeammateLoop failed: %v", err)
	}
	defer tm.StopTeammateLoop("worker-bob")

	// Send message
	req := TeamMessage{
		Sender:    "boss",
		Content:   "Fix production",
		Timestamp: 2000.0,
	}

	if err := tm.AppendToInbox("worker-bob", req); err != nil {
		t.Fatalf("AppendToInbox failed: %v", err)
	}

	// Wait for loop to pick it up and process
	select {
	case msg := <-processed:
		if msg.Sender != "boss" || msg.Content != "Fix production" {
			t.Errorf("unexpected message processed: %+v", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for background loop processing")
	}

	// Check reply inbox of "boss"
	time.Sleep(100 * time.Millisecond)
	replies, err := tm.ReadAndClearInbox("boss")
	if err != nil {
		t.Fatalf("ReadAndClearInbox boss failed: %v", err)
	}

	if len(replies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(replies))
	}

	if replies[0].Sender != "worker-bob" || replies[0].Content != "Done processing: Fix production" {
		t.Errorf("unexpected reply contents: %+v", replies[0])
	}
}

func TestTeamManager_BroadcastAndList(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-team-bc-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	tm := &TeamManager{
		teamDir:   tempDir,
		teammates: make(map[string]*Teammate),
	}
	_ = os.MkdirAll(filepath.Join(tempDir, "inbox"), 0755)

	_, _ = tm.RegisterTeammate("bob", "Dev", "")
	_, _ = tm.RegisterTeammate("alice", "QA", "")

	list, err := tm.ListTeammates()
	if err != nil {
		t.Fatalf("ListTeammates failed: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 teammates, got %d", len(list))
	}

	if err := tm.Broadcast("boss", "All hands meeting!"); err != nil {
		t.Fatalf("Broadcast failed: %v", err)
	}

	bobMsgs, _ := tm.ReadAndClearInbox("bob")
	if len(bobMsgs) != 1 || bobMsgs[0].Content != "All hands meeting!" || bobMsgs[0].Sender != "boss" {
		t.Errorf("bob received unexpected messages: %+v", bobMsgs)
	}

	aliceMsgs, _ := tm.ReadAndClearInbox("alice")
	if len(aliceMsgs) != 1 || aliceMsgs[0].Content != "All hands meeting!" || aliceMsgs[0].Sender != "boss" {
		t.Errorf("alice received unexpected messages: %+v", aliceMsgs)
	}
}
