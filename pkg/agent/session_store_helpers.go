package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/session"
)
func estimateTokens(textLen int) int {
	if textLen <= 0 {
		return 0
	}
	return textLen / 4
}

// estimateCost returns a rough USD cost from token count.
// Uses $2.00 per million tokens as baseline.
func estimateCost(tokens int) float64 {
	return float64(tokens) * 2.0 / 1000000.0
}

// estimateEventTextLen sums text length across all events.
func estimateEventTextLen(events []*session.Event) int {
	total := 0
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if ev.Content != nil {
			for _, part := range ev.Content.Parts {
				total += len(part.Text)
			}
		}
		if ev.LLMResponse.Content != nil {
			for _, part := range ev.LLMResponse.Content.Parts {
				total += len(part.Text)
			}
		}
	}
	return total
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

// ValidateResume checks session integrity and returns warnings for any issues found.
func (s *SerializedSession) ValidateResume() []string {
	var warnings []string
	if len(s.Events) == 0 {
		warnings = append(warnings, "session has no events")
	}
	if s.CWD == "" {
		warnings = append(warnings, "session has no CWD recorded")
	} else if _, err := os.Stat(s.CWD); os.IsNotExist(err) {
		warnings = append(warnings, fmt.Sprintf("session CWD no longer exists: %s", s.CWD))
	}
	if s.State == nil {
		warnings = append(warnings, "session has no state map")
	}
	if s.CompactionArchivePath != "" {
		if _, err := os.Stat(s.CompactionArchivePath); os.IsNotExist(err) {
			warnings = append(warnings, "compaction archive no longer exists: "+s.CompactionArchivePath)
		}
	}
	return warnings
}

// Ensure interface matching
var _ session.Service = (*PersistentSessionService)(nil)
