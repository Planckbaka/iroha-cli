package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	{regexp.MustCompile(`(?i)\b(sk-[a-zA-Z0-9_-]{20,})\b`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)\b(AIzaSy[a-zA-Z0-9_-]{30,})\b`), "[REDACTED]"},
	{regexp.MustCompile(`(?i)\b(bearer\s+)[a-zA-Z0-9_\-\.\~]{10,}`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)(x-api-key\s*:\s*)[a-zA-Z0-9_-]+`), `$1[REDACTED]`},
	{regexp.MustCompile(`(?i)"(api_key|token|password|secret|key|api-key)"\s*:\s*"[^"]+"`), `"$1":"[REDACTED]"`},
	{regexp.MustCompile(`(?i)(api_key|token|password|secret|key|api-key)\s*=\s*[^\s&"\n]+`), `$1=[REDACTED]`},
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

// ToolTrace represents a single structured tool-trace log line for observability.
type ToolTrace struct {
	Timestamp          string `json:"timestamp"`
	SessionID          string `json:"session_id"`
	Tool               string `json:"tool"`
	ArgsHash           string `json:"args_hash"`
	ResultStatus       string `json:"result_status"`
	DurationMS         int64  `json:"duration_ms"`
	Tier               string `json:"tier,omitempty"`
	PermissionDecision string `json:"permission_decision"`
}

// traceLogger manages the dedicated trace JSONL file and auto-cleanup.
type traceLogger struct {
	mu        sync.Mutex
	sessionID string
	file      *os.File
}

var globalTraceLogger = &traceLogger{}

const traceRetentionDays = 7

// LogToolTrace records a structured tool-trace event to the session's trace JSONL file.
// It hashes args for privacy, auto-creates the logs directory, and cleans up files older
// than 7 days on each open.
func LogToolTrace(tool string, args any, resultStatus string, durationMS int64) {
	globalTraceLogger.mu.Lock()
	defer globalTraceLogger.mu.Unlock()

	sessionID := GlobalLogger.sessionID
	if sessionID == "" {
		sessionID = "uninitialized"
	}

	// Initialize or switch trace file if session changed
	if globalTraceLogger.sessionID != sessionID || globalTraceLogger.file == nil {
		if globalTraceLogger.file != nil {
			_ = globalTraceLogger.file.Close()
			globalTraceLogger.file = nil
		}
		globalTraceLogger.sessionID = sessionID

		logsDir := GlobalLogger.logsDir
		_ = os.MkdirAll(logsDir, 0755)

		// Auto-cleanup: delete trace files older than 7 days
		cleanupOldTraceFiles(logsDir)

		tracePath := filepath.Join(logsDir, fmt.Sprintf("trace-%s.jsonl", sessionID))
		f, err := os.OpenFile(tracePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return
		}
		globalTraceLogger.file = f
	}

	// Hash args for privacy
	argsHash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%v", args))))[:16]

	trace := ToolTrace{
		Timestamp:    time.Now().Format(time.RFC3339),
		SessionID:    sessionID,
		Tool:         tool,
		ArgsHash:     argsHash,
		ResultStatus: resultStatus,
		DurationMS:   durationMS,
	}

	bytes, err := json.Marshal(trace)
	if err != nil {
		return
	}

	_, _ = globalTraceLogger.file.Write(append(bytes, '\n'))
}

// cleanupOldTraceFiles removes trace JSONL files older than traceRetentionDays.
func cleanupOldTraceFiles(logsDir string) {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -traceRetentionDays)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasPrefix(entry.Name(), "trace-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(logsDir, entry.Name()))
		}
	}
}

// ReadTraceTail reads the last n lines from the current session's trace file.
func ReadTraceTail(sessionID string, n int) ([]ToolTrace, error) {
	logsDir := GlobalLogger.logsDir
	tracePath := filepath.Join(logsDir, fmt.Sprintf("trace-%s.jsonl", sessionID))

	data, err := os.ReadFile(tracePath)
	if err != nil {
		return nil, fmt.Errorf("no trace data for session %s: %w", sessionID, err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("trace file is empty")
	}

	// Take the last n lines
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	lines = lines[start:]

	var traces []ToolTrace
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var t ToolTrace
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			continue
		}
		traces = append(traces, t)
	}

	return traces, nil
}
