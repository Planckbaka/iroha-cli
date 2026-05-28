package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// ─── Pure helper function tests ──────────────────────────────────────────────

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name    string
		textLen int
		want    int
	}{
		{"negative", -1, 0},
		{"zero", 0, 0},
		{"one_char", 1, 0},
		{"three_chars", 3, 0},
		{"four_chars", 4, 1},
		{"eight_chars", 8, 2},
		{"large", 4000, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := estimateTokens(tt.textLen); got != tt.want {
				t.Errorf("estimateTokens(%d) = %d, want %d", tt.textLen, got, tt.want)
			}
		})
	}
}

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		name   string
		tokens int
		want   float64
	}{
		{"zero", 0, 0.0},
		{"one_million", 1_000_000, 2.0},
		{"five_hundred_k", 500_000, 1.0},
		{"one_token", 1, 0.000002},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateCost(tt.tokens)
			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 1e-9 {
				t.Errorf("estimateCost(%d) = %f, want %f", tt.tokens, got, tt.want)
			}
		})
	}
}

func TestEstimateEventTextLen(t *testing.T) {
	tests := []struct {
		name   string
		events []*session.Event
		want   int
	}{
		{"nil_slice", nil, 0},
		{"empty_slice", []*session.Event{}, 0},
		{"nil_event", []*session.Event{nil}, 0},
		{"single_event_with_content", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv-1")
				ev.Content = &genai.Content{Parts: []*genai.Part{{Text: "hello world"}}}
				return ev
			}(),
		}, 22},
		{"event_with_llm_response_content", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv-2")
				ev.LLMResponse = model.LLMResponse{
					Content: &genai.Content{Parts: []*genai.Part{{Text: "response text"}}},
				}
				return ev
			}(),
		}, 26},
		{"mixed_events", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv-3")
				ev.Content = &genai.Content{Parts: []*genai.Part{{Text: "user"}}}
				return ev
			}(),
			func() *session.Event {
				ev := session.NewEvent("inv-4")
				ev.LLMResponse = model.LLMResponse{
					Content: &genai.Content{Parts: []*genai.Part{{Text: "assistant"}}},
				}
				return ev
			}(),
		}, 8 + 18},
		{"nil_parts_in_content", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv-5")
				ev.Content = &genai.Content{}
				return ev
			}(),
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := estimateEventTextLen(tt.events); got != tt.want {
				t.Errorf("estimateEventTextLen() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetFirstPrompt(t *testing.T) {
	tests := []struct {
		name   string
		events []*session.Event
		want   string
	}{
		{"nil_events", nil, "New Session"},
		{"empty_events", []*session.Event{}, "New Session"},
		{"normal_short", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv")
				ev.Content = &genai.Content{Parts: []*genai.Part{{Text: "Hello"}}}
				return ev
			}(),
		}, "Hello"},
		{"long_text_truncated", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv")
				longText := strings.Repeat("a", 100)
				ev.Content = &genai.Content{Parts: []*genai.Part{{Text: longText}}}
				return ev
			}(),
		}, strings.Repeat("a", 57) + "..."},
		{"exactly_60_chars", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv")
				text60 := strings.Repeat("x", 60)
				ev.Content = &genai.Content{Parts: []*genai.Part{{Text: text60}}}
				return ev
			}(),
		}, strings.Repeat("x", 60)},
		{"whitespace_only_falls_through", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv")
				ev.Content = &genai.Content{Parts: []*genai.Part{{Text: "   "}}}
				return ev
			}(),
		}, "New Session"},
		{"background_results_prefix_skipped", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv")
				ev.Content = &genai.Content{Parts: []*genai.Part{{Text: "<background-results>\n  <task id=\"t1\"/>\n</background-results>\nreal prompt here"}}}
				return ev
			}(),
		}, "real prompt here"},
		{"empty_part_skipped", []*session.Event{
			func() *session.Event {
				ev := session.NewEvent("inv")
				ev.Content = &genai.Content{Parts: []*genai.Part{{Text: ""}, {Text: "second part"}}}
				return ev
			}(),
		}, "second part"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getFirstPrompt(tt.events); got != tt.want {
				t.Errorf("getFirstPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanOldSessions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-clean-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create an "old" file
	oldPath := filepath.Join(tmpDir, "old-session.json")
	if err := os.WriteFile(oldPath, []byte(`{"id":"old"}`), 0644); err != nil {
		t.Fatalf("failed to write old file: %v", err)
	}
	// Set its mod time to 2 days ago
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("failed to change mod time: %v", err)
	}

	// Create a "recent" file
	recentPath := filepath.Join(tmpDir, "recent-session.json")
	if err := os.WriteFile(recentPath, []byte(`{"id":"recent"}`), 0644); err != nil {
		t.Fatalf("failed to write recent file: %v", err)
	}

	// Create a non-json file (should be ignored)
	nonJSONPath := filepath.Join(tmpDir, "notes.txt")
	if err := os.WriteFile(nonJSONPath, []byte("notes"), 0644); err != nil {
		t.Fatalf("failed to write non-json file: %v", err)
	}

	// Create a subdirectory (should be ignored)
	if err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	count := CleanOldSessions(tmpDir, 24*time.Hour)
	if count != 1 {
		t.Errorf("expected 1 cleaned file, got %d", count)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("expected old file to be deleted")
	}
	if _, err := os.Stat(recentPath); os.IsNotExist(err) {
		t.Error("expected recent file to still exist")
	}

	// Non-existent directory returns 0
	count = CleanOldSessions(filepath.Join(tmpDir, "nonexistent"), 24*time.Hour)
	if count != 0 {
		t.Errorf("expected 0 for non-existent dir, got %d", count)
	}
}

func TestGetSessionsDir(t *testing.T) {
	dir := GetSessionsDir()
	if dir == "" {
		t.Error("expected non-empty sessions dir")
	}
	// Should end with ".iroha/sessions"
	if !strings.HasSuffix(dir, ".iroha"+string(filepath.Separator)+"sessions") && !strings.Contains(dir, "sessions") {
		t.Errorf("expected sessions dir to contain 'sessions', got %s", dir)
	}
}

func TestValidateResume(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-validate-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	existingCWD := tmpDir
	nonExistentCWD := filepath.Join(tmpDir, "no-such-dir")

	tests := []struct {
		name              string
		session           SerializedSession
		wantWarningCount  int
		wantWarningSubstr string
	}{
		{
			"no_events",
			SerializedSession{CWD: existingCWD, State: map[string]any{"k": "v"}},
			1, "no events",
		},
		{
			"no_cwd",
			SerializedSession{
				Events: []*session.Event{session.NewEvent("inv")},
				State:  map[string]any{"k": "v"},
			},
			1, "no CWD",
		},
		{
			"cwd_not_exist",
			SerializedSession{
				CWD:    nonExistentCWD,
				Events: []*session.Event{session.NewEvent("inv")},
				State:  map[string]any{"k": "v"},
			},
			1, "no longer exists",
		},
		{
			"no_state",
			SerializedSession{
				CWD:    existingCWD,
				Events: []*session.Event{session.NewEvent("inv")},
			},
			1, "no state",
		},
		{
			"compaction_archive_missing",
			SerializedSession{
				CWD:                    existingCWD,
				Events:                 []*session.Event{session.NewEvent("inv")},
				State:                  map[string]any{"k": "v"},
				CompactionArchivePath:  filepath.Join(tmpDir, "missing.jsonl"),
			},
			1, "compaction archive",
		},
		{
			"all_valid",
			SerializedSession{
				CWD:    existingCWD,
				Events: []*session.Event{session.NewEvent("inv")},
				State:  map[string]any{"k": "v"},
			},
			0, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := tt.session.ValidateResume()
			if len(warnings) != tt.wantWarningCount {
				t.Errorf("ValidateResume() returned %d warnings, want %d: %v", len(warnings), tt.wantWarningCount, warnings)
			}
			if tt.wantWarningSubstr != "" && len(warnings) > 0 {
				if !strings.Contains(warnings[0], tt.wantWarningSubstr) {
					t.Errorf("expected warning to contain %q, got %q", tt.wantWarningSubstr, warnings[0])
				}
			}
		})
	}
}

// ─── PersistentSessionService integration tests ──────────────────────────────

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

	// Test 6: Verify SessionMetadata TotalTokens and TotalCost via mock session JSON
	mockJSON := `{
		"id": "sess-mock",
		"cwd": "/mock/path",
		"first_prompt": "Hello Doctor",
		"events": [
			{
				"content": {
					"parts": [
						{"text": "This is a mock event prompt to test the token calculation in ListSavedSessions of persistent store"}
					]
				}
			}
		]
	}`
	err = os.WriteFile(filepath.Join(tmpDir, "sess-mock.json"), []byte(mockJSON), 0644)
	if err != nil {
		t.Fatalf("failed to write mock session file: %v", err)
	}

	metaList, err = pService.ListSavedSessions()
	if err != nil {
		t.Fatalf("failed to list sessions: %v", err)
	}
	foundMock := false
	for _, m := range metaList {
		if m.ID == "sess-mock" {
			foundMock = true
			if m.TotalTokens <= 0 {
				t.Errorf("expected TotalTokens to be calculated, got %d", m.TotalTokens)
			}
			if m.TotalCost <= 0 {
				t.Errorf("expected TotalCost to be calculated, got %f", m.TotalCost)
			}
		}
	}
	if !foundMock {
		t.Error("expected to find sess-mock in metadata list")
	}
}

func TestPersistentSessionService_Delete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-delete-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	inMem := session.InMemoryService()
	ps := NewPersistentSessionService(inMem, tmpDir)

	_, err = ps.Create(ctx, &session.CreateRequest{
		AppName: "test-app", UserID: "test-user", SessionID: "sess-del", State: map[string]any{},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	filePath := filepath.Join(tmpDir, "sess-del.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("session file not created")
	}

	err = ps.Delete(ctx, &session.DeleteRequest{
		AppName: "test-app", UserID: "test-user", SessionID: "sess-del",
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("expected session file to be deleted")
	}
}

func TestPersistentSessionService_DeleteNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-del-ne-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	inMem := session.InMemoryService()
	ps := NewPersistentSessionService(inMem, tmpDir)

	_, err = inMem.Create(ctx, &session.CreateRequest{
		AppName: "test-app", UserID: "test-user", SessionID: "sess-del-ne", State: map[string]any{},
	})
	if err != nil {
		t.Fatalf("create in delegate: %v", err)
	}

	err = ps.Delete(ctx, &session.DeleteRequest{
		AppName: "test-app", UserID: "test-user", SessionID: "sess-del-ne",
	})
	if err != nil {
		t.Fatalf("delete non-existent should not error: %v", err)
	}
}

func TestPersistentSessionService_LoadSessionsCorruptFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-load-corrupt-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "corrupt.json"), []byte(`{invalid json`), 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	validData := SerializedSession{
		ID: "sess-valid", AppName: "test-app", UserID: "test-user",
		State: map[string]any{}, Events: []*session.Event{},
	}
	validBytes, _ := json.MarshalIndent(validData, "", "  ")
	if err := os.WriteFile(filepath.Join(tmpDir, "sess-valid.json"), validBytes, 0644); err != nil {
		t.Fatalf("write valid: %v", err)
	}

	ctx := context.Background()
	inMem := session.InMemoryService()
	ps := NewPersistentSessionService(inMem, tmpDir)

	err = ps.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("LoadSessions should not fail on individual corrupt files: %v", err)
	}

	getRes, err := ps.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: "sess-valid",
	})
	if err != nil {
		t.Fatalf("expected valid session to be loaded: %v", err)
	}
	if getRes.Session.ID() != "sess-valid" {
		t.Errorf("expected sess-valid, got %s", getRes.Session.ID())
	}
}

func TestPersistentSessionService_LoadSessionsEmptyDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-load-empty-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	inMem := session.InMemoryService()
	ps := NewPersistentSessionService(inMem, tmpDir)

	err = ps.LoadSessions(ctx)
	if err != nil {
		t.Fatalf("LoadSessions on empty dir should not error: %v", err)
	}
}

func TestPersistentSessionService_ForkSessionNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-fork-ne-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx := context.Background()
	inMem := session.InMemoryService()
	ps := NewPersistentSessionService(inMem, tmpDir)

	err = ps.ForkSession(ctx, "non-existent-id", "new-id")
	if err == nil {
		t.Fatal("expected error when forking non-existent session")
	}
}

func TestPersistentSessionService_ListSavedSessionsSortOrder(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-list-sort-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	older := SerializedSession{
		ID: "sess-older", AppName: "test", UserID: "u",
		LastUpdateTime: time.Now().Add(-1 * time.Hour),
		State: map[string]any{}, Events: []*session.Event{},
	}
	newer := SerializedSession{
		ID: "sess-newer", AppName: "test", UserID: "u",
		LastUpdateTime: time.Now(),
		State: map[string]any{}, Events: []*session.Event{},
	}

	for _, s := range []SerializedSession{older, newer} {
		data, _ := json.MarshalIndent(s, "", "  ")
		if err := os.WriteFile(filepath.Join(tmpDir, s.ID+".json"), data, 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	inMem := session.InMemoryService()
	ps := NewPersistentSessionService(inMem, tmpDir)

	metaList, err := ps.ListSavedSessions()
	if err != nil {
		t.Fatalf("ListSavedSessions: %v", err)
	}
	if len(metaList) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(metaList))
	}
	if metaList[0].ID != "sess-newer" {
		t.Errorf("expected newest first, got %s", metaList[0].ID)
	}
	if metaList[1].ID != "sess-older" {
		t.Errorf("expected oldest second, got %s", metaList[1].ID)
	}
}

func TestSerializedSession_JSONRoundTrip(t *testing.T) {
	original := SerializedSession{
		ID: "sess-rt", AppName: "app", UserID: "user",
		LastUpdateTime:        time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		State:                 map[string]any{"key": "value"},
		CWD:                   "/home/user/project",
		FirstPrompt:           "Hello",
		PermissionMode:        "auto",
		TotalTokens:           500,
		TotalCost:             0.001,
		CompactionArchivePath: "/archive/sess-rt.jsonl",
	}
	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded SerializedSession
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID: got %q, want %q", decoded.ID, original.ID)
	}
	if decoded.AppName != original.AppName {
		t.Errorf("AppName: got %q, want %q", decoded.AppName, original.AppName)
	}
	if decoded.PermissionMode != original.PermissionMode {
		t.Errorf("PermissionMode: got %q, want %q", decoded.PermissionMode, original.PermissionMode)
	}
	if decoded.TotalTokens != original.TotalTokens {
		t.Errorf("TotalTokens: got %d, want %d", decoded.TotalTokens, original.TotalTokens)
	}
	if decoded.CWD != original.CWD {
		t.Errorf("CWD: got %q, want %q", decoded.CWD, original.CWD)
	}
}

func TestNewPersistentSessionService_CreatesDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "iroha-newdir-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "nested", "sessions")
	inMem := session.InMemoryService()
	_ = NewPersistentSessionService(inMem, subDir)

	if _, err := os.Stat(subDir); os.IsNotExist(err) {
		t.Error("expected sessions directory to be created")
	}
}

func TestSessionMetadata_JSONRoundTrip(t *testing.T) {
	original := SessionMetadata{
		ID: "meta-1", CWD: "/project", FirstPrompt: "Build it",
		LastUpdateTime: time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		TotalTokens: 1000, TotalCost: 0.002,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded SessionMetadata
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ID != original.ID || decoded.CWD != original.CWD || decoded.TotalTokens != original.TotalTokens {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
}
