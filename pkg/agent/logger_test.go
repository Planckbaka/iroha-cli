package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "OpenAI API Key",
			input:    "my api key is sk-abcdefghijklmnopqrstuvwxyz0123456789",
			expected: "my api key is [REDACTED]",
		},
		{
			name:     "Bearer Token",
			input:    "Authorization: bearer abc123XYZ_-.~something",
			expected: "Authorization: Bearer [REDACTED]",
		},
		{
			name:     "JSON API Key field",
			input:    `{"api_key": "sensitive-value-here", "status": "ok"}`,
			expected: `{"api_key":"[REDACTED]", "status": "ok"}`,
		},
		{
			name:     "JSON Token field",
			input:    `{"token": "someSecretToken", "user": "admin"}`,
			expected: `{"token":"[REDACTED]", "user": "admin"}`,
		},
		{
			name:     "URL Query or Env Equal Sign Key",
			input:    "api_key=mysecretkey&other=val",
			expected: "api_key=[REDACTED]&other=val",
		},
		{
			name:     "URL Query Token",
			input:    "token=my_secret_token_123",
			expected: "token=[REDACTED]",
		},
		{
			name:     "Plain Text Key Value",
			input:    "secret=supersecurepassword123",
			expected: "secret=[REDACTED]",
		},
		{
			name:     "Google/Gemini API Key",
			input:    "key=AIzaSyABCdefGHIjklMNOpqrsTUVwxyz1234567890",
			expected: "key=[REDACTED]",
		},
		{
			name:     "Anthropic API Key with sk-ant- prefix",
			input:    "using key sk-ant-api03-abcdefghijklmnopqrstuvwxyz123456",
			expected: "using key [REDACTED]",
		},
		{
			name:     "x-api-key header",
			input:    "x-api-key: sk-ant-api03-my-anthropic-key-here12345",
			expected: "x-api-key: [REDACTED]",
		},
		{
			name:     "JSON api-key field with hyphen",
			input:    `{"api-key": "sensitive-value-here", "status": "ok"}`,
			expected: `{"api-key":"[REDACTED]", "status": "ok"}`,
		},
		{
			name:     "env-style api-key assignment",
			input:    "api-key=my-secret-key-12345",
			expected: "api-key=[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactSecrets(tt.input)
			if got != tt.expected {
				t.Errorf("RedactSecrets() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestLoggerManager_ConcurrentAndJSONL(t *testing.T) {
	// Create a temporary directory for test logs
	tempDir, err := os.MkdirTemp("", "iroha_logger_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	lm := &LoggerManager{
		logsDir: tempDir,
	}
	lm.SetSessionID("test_session_123")

	const (
		goroutines = 10
		logsPerGo  = 20
	)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < logsPerGo; j++ {
				metadata := map[string]any{
					"worker_id": id,
					"loop_idx":  j,
					"api_key":   "sk-testkey12345678901234567890", // should be redacted
				}
				lm.Log(LevelInfo, CatSystem, "test_event", fmt.Sprintf("message from %d-%d", id, j), 15, metadata)
			}
		}(i)
	}

	wg.Wait()

	// Ensure files are closed to flush buffers
	lm.SetSessionID("")

	// Verify JSONL File
	jsonlPath := filepath.Join(tempDir, "session_test_session_123_audit.jsonl")
	jFile, err := os.Open(jsonlPath)
	if err != nil {
		t.Fatalf("failed to open jsonl audit file: %v", err)
	}
	defer jFile.Close()

	scanner := bufio.NewScanner(jFile)
	logCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		logCount++

		// Verify redact worked on JSONL stream-level outcome
		if strings.Contains(line, "sk-testkey12345678901234567890") {
			t.Errorf("unredacted API key found in line: %s", line)
		}

		var record AuditLogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Errorf("failed to unmarshal JSONL record: %v, line: %s", err, line)
		}

		if record.SessionID != "test_session_123" {
			t.Errorf("expected session_id 'test_session_123', got %q", record.SessionID)
		}
		if record.Level != LevelInfo {
			t.Errorf("expected level 'INFO', got %q", record.Level)
		}
		if record.Category != CatSystem {
			t.Errorf("expected category 'system', got %q", record.Category)
		}
		if record.Event != "test_event" {
			t.Errorf("expected event 'test_event', got %q", record.Event)
		}
		if record.DurationMS != 15 {
			t.Errorf("expected duration_ms 15, got %d", record.DurationMS)
		}

		// Check redacted metadata
		workerVal, hasWorker := record.Metadata["worker_id"]
		if !hasWorker {
			t.Errorf("missing 'worker_id' in metadata")
		} else {
			if _, ok := workerVal.(float64); !ok {
				t.Errorf("worker_id is not a float64 (unmarshalled from JSON)")
			}
		}

		apiKeyVal, hasKey := record.Metadata["api_key"]
		if !hasKey {
			t.Errorf("missing 'api_key' in metadata")
		} else {
			if apiKeyVal != "[REDACTED]" {
				t.Errorf("expected redacted api_key, got %v", apiKeyVal)
			}
		}
	}

	expectedLogs := goroutines * logsPerGo
	if logCount != expectedLogs {
		t.Errorf("expected %d logs, got %d", expectedLogs, logCount)
	}

	// Verify Plain-text Log File
	plainPath := filepath.Join(tempDir, "session_test_session_123_audit.log")
	pFile, err := os.Open(plainPath)
	if err != nil {
		t.Fatalf("failed to open plain audit file: %v", err)
	}
	defer pFile.Close()

	pScanner := bufio.NewScanner(pFile)
	plainLogCount := 0
	for pScanner.Scan() {
		line := pScanner.Text()
		plainLogCount++

		if strings.Contains(line, "sk-testkey12345678901234567890") {
			t.Errorf("unredacted API key found in plain log line: %s", line)
		}
		if !strings.Contains(line, "[INFO]") || !strings.Contains(line, "[system]") || !strings.Contains(line, "test_event") {
			t.Errorf("plain text log line structured incorrectly: %s", line)
		}
		if !strings.Contains(line, `duration=15ms`) {
			t.Errorf("plain text log line missing duration: %s", line)
		}
	}

	if plainLogCount != expectedLogs {
		t.Errorf("expected %d plain logs, got %d", expectedLogs, plainLogCount)
	}
}
