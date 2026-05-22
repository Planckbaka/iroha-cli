package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

type LogLevel string

const (
	LevelInfo  LogLevel = "INFO"
	LevelWarn  LogLevel = "WARN"
	LevelError LogLevel = "ERROR"
	LevelAudit LogLevel = "AUDIT"
)

type LogCategory string

const (
	CatUserInput LogCategory = "user_input"
	CatTUI       LogCategory = "tui"
	CatToolCall  LogCategory = "tool_call"
	CatSecurity  LogCategory = "security_gate"
	CatSession   LogCategory = "session"
	CatSubagent  LogCategory = "subagent"
	CatSystem    LogCategory = "system"
)

var secretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)\b(sk-[a-zA-Z0-9]{20,})\b`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)\b(bearer\s+[a-zA-Z0-9_\-\.\~]{10,})\b`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)"(api_key|token|password|secret|key)"\s*:\s*"[^"]+"`), `"$1":"[REDACTED]"`},
	{regexp.MustCompile(`(?i)(api_key|token|password|secret|key)\s*=\s*[^\s&"\n]+`), `$1=[REDACTED]`},
}

// RedactSecrets applies regular expressions to mask API keys and passwords.
func RedactSecrets(text string) string {
	for _, sp := range secretPatterns {
		text = sp.pattern.ReplaceAllString(text, sp.replacement)
	}
	return text
}

// AuditLogRecord represents a single structured log line in JSONL format.
type AuditLogRecord struct {
	Timestamp  string         `json:"timestamp"`
	Level      LogLevel       `json:"level"`
	SessionID  string         `json:"session_id"`
	Category   LogCategory    `json:"category"`
	Event      string         `json:"event"`
	Message    string         `json:"message"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// LoggerManager manages dual log writers for structured JSONL and plain-text.
type LoggerManager struct {
	mu        sync.Mutex
	sessionID string
	jsonlFile *os.File
	plainFile *os.File
	logsDir   string
}

// GlobalLogger is the centralized singleton for writing audit and diagnostic logs.
var GlobalLogger = &LoggerManager{
	logsDir: filepath.Join(".", ".iroha", "logs"),
}

// SetSessionID configures the active session ID and initializes the log files.
func (lm *LoggerManager) SetSessionID(sessionID string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Close existing files if open
	if lm.jsonlFile != nil {
		_ = lm.jsonlFile.Close()
		lm.jsonlFile = nil
	}
	if lm.plainFile != nil {
		_ = lm.plainFile.Close()
		lm.plainFile = nil
	}

	lm.sessionID = sessionID
	if sessionID == "" {
		return
	}

	// Ensure directory exists
	_ = os.MkdirAll(lm.logsDir, 0755)

	// Create/Open structured JSONL file
	jsonlPath := filepath.Join(lm.logsDir, fmt.Sprintf("session_%s_audit.jsonl", sessionID))
	jFile, err := os.OpenFile(jsonlPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		lm.jsonlFile = jFile
	}

	// Create/Open plain text file
	plainPath := filepath.Join(lm.logsDir, fmt.Sprintf("session_%s_audit.log", sessionID))
	pFile, err := os.OpenFile(plainPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		lm.plainFile = pFile
	}
}

// Log records a structured log to both JSONL and plain-text.
func (lm *LoggerManager) Log(level LogLevel, category LogCategory, event string, message string, durationMS int64, metadata map[string]any) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// If files are not initialized, do a quick lazy init with a default or skip
	if lm.jsonlFile == nil && lm.plainFile == nil {
		if lm.sessionID == "" {
			lm.sessionID = "uninitialized"
		}
		_ = os.MkdirAll(lm.logsDir, 0755)
		jsonlPath := filepath.Join(lm.logsDir, fmt.Sprintf("session_%s_audit.jsonl", lm.sessionID))
		jFile, err := os.OpenFile(jsonlPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			lm.jsonlFile = jFile
		}
		plainPath := filepath.Join(lm.logsDir, fmt.Sprintf("session_%s_audit.log", lm.sessionID))
		pFile, err := os.OpenFile(plainPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			lm.plainFile = pFile
		}
	}

	ts := time.Now().Format(time.RFC3339)
	record := AuditLogRecord{
		Timestamp:  ts,
		Level:      level,
		SessionID:  lm.sessionID,
		Category:   category,
		Event:      event,
		Message:    message,
		DurationMS: durationMS,
		Metadata:   metadata,
	}

	// 1. Write structured JSON Lines
	if lm.jsonlFile != nil {
		bytes, err := json.Marshal(record)
		if err == nil {
			redacted := RedactSecrets(string(bytes))
			_, _ = lm.jsonlFile.Write(append([]byte(redacted), '\n'))
		}
	}

	// 2. Write beautiful plain text log
	if lm.plainFile != nil {
		var metaStr string
		if len(metadata) > 0 {
			metaBytes, err := json.Marshal(metadata)
			if err == nil {
				metaStr = fmt.Sprintf(" | metadata=%s", string(metaBytes))
			}
		}

		var durStr string
		if durationMS > 0 {
			durStr = fmt.Sprintf(" | duration=%dms", durationMS)
		}

		plainMsg := fmt.Sprintf("[%s] [%s] [%s] [%s] %s%s%s\n",
			ts, level, category, event, message, durStr, metaStr)
		redactedPlain := RedactSecrets(plainMsg)
		_, _ = lm.plainFile.WriteString(redactedPlain)
	}
}

// LogInfo helper for LevelInfo
func LogInfo(category LogCategory, event string, message string, metadata map[string]any) {
	GlobalLogger.Log(LevelInfo, category, event, message, 0, metadata)
}

// LogWarn helper for LevelWarn
func LogWarn(category LogCategory, event string, message string, metadata map[string]any) {
	GlobalLogger.Log(LevelWarn, category, event, message, 0, metadata)
}

// LogError helper for LevelError
func LogError(category LogCategory, event string, message string, err error, metadata map[string]any) {
	if metadata == nil {
		metadata = make(map[string]any)
	}
	if err != nil {
		metadata["error"] = err.Error()
	}
	GlobalLogger.Log(LevelError, category, event, message, 0, metadata)
}

// LogAudit helper for LevelAudit
func LogAudit(category LogCategory, event string, message string, metadata map[string]any) {
	GlobalLogger.Log(LevelAudit, category, event, message, 0, metadata)
}
