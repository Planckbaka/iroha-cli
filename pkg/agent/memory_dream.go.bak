package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// DreamConsolidator automates persistent memory consolidation (deduplication, pruning, merging).
// It runs when 7 gates are satisfied, utilizing a PID-based file lock (.dream_lock) for multi-session safety.
type DreamConsolidator struct {
	mu                sync.Mutex
	Enabled           bool
	Cooldown          time.Duration
	Throttle          time.Duration
	MinSessions       int
	LastConsolidation time.Time
	LastScan          time.Time
	SessionCount      int
}

// NewDreamConsolidator creates a default DreamConsolidator.
func NewDreamConsolidator() *DreamConsolidator {
	return &DreamConsolidator{
		Enabled:     true,
		Cooldown:    24 * time.Hour,
		Throttle:    10 * time.Minute,
		MinSessions: 5,
	}
}

// IncrementSession increments the session counter.
func (dc *DreamConsolidator) IncrementSession() {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.SessionCount++
}

// isProcessAlive checks if a process with given PID is still active on UNIX/macOS systems.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// sending signal 0 checks for process existence on Unix-like systems
	return proc.Signal(syscall.Signal(0)) == nil
}

// acquireLock tries to write a PID-based file lock .dream_lock in the target directory.
func (dc *DreamConsolidator) acquireLock(dir string) (bool, error) {
	lockPath := filepath.Join(dir, ".dream_lock")
	data, err := os.ReadFile(lockPath)
	if err == nil {
		content := strings.TrimSpace(string(data))
		parts := strings.Split(content, ":")
		if len(parts) == 2 {
			pidVal, _ := strconv.Atoi(parts[0])
			timeVal, _ := strconv.ParseInt(parts[1], 10, 64)

			// Check if lock process is alive
			alive := false
			if pidVal > 0 {
				alive = isProcessAlive(pidVal)
			}

			// If process is still alive and lock is within 1 hour, lock is valid (cannot acquire)
			if alive && time.Since(time.Unix(timeVal, 0)) < 1*time.Hour {
				return false, nil
			}

			// Otherwise, remove stale lock
			_ = os.Remove(lockPath)
		}
	}

	// Write new lock file
	lockContent := fmt.Sprintf("%d:%d", os.Getpid(), time.Now().Unix())
	if err := os.WriteFile(lockPath, []byte(lockContent), 0600); err != nil {
		return false, err
	}
	return true, nil
}

// releaseLock deletes the PID-based .dream_lock file if owned by the current process.
func (dc *DreamConsolidator) releaseLock(dir string) {
	lockPath := filepath.Join(dir, ".dream_lock")
	data, err := os.ReadFile(lockPath)
	if err == nil {
		content := strings.TrimSpace(string(data))
		parts := strings.Split(content, ":")
		if len(parts) > 0 {
			pidVal, _ := strconv.Atoi(parts[0])
			if pidVal == os.Getpid() {
				_ = os.Remove(lockPath)
			}
		}
	}
}

// ShouldConsolidate evaluates the 7 validation gates to see if consolidation is allowed.
// If force is true, Gates 4 (cooldown), 5 (scan throttle), and 6 (session count) are bypassed.
func (dc *DreamConsolidator) ShouldConsolidate(mm *MemoryManager, force bool) (bool, string) {
	if !dc.Enabled {
		return false, "Gate 1: consolidation is disabled"
	}

	saveDir, err := projectMemoryDir()
	if err != nil {
		return false, "Gate 2: failed to resolve memory directory"
	}

	files, err := os.ReadDir(saveDir)
	if err != nil {
		return false, "Gate 2: memory directory does not exist"
	}

	hasMemories := false
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") && f.Name() != "MEMORY.md" && f.Name() != ".dream_lock" {
			hasMemories = true
			break
		}
	}
	if !hasMemories {
		return false, "Gate 2: no memory files found"
	}

	if GlobalPermissionManager.GetMode() == ModePlan {
		return false, "Gate 3: plan mode does not allow consolidation"
	}

	if !force {
		if !dc.LastConsolidation.IsZero() && time.Since(dc.LastConsolidation) < dc.Cooldown {
			remaining := dc.Cooldown - time.Since(dc.LastConsolidation)
			return false, fmt.Sprintf("Gate 4: cooldown active, %v remaining", remaining.Round(time.Second))
		}

		if !dc.LastScan.IsZero() && time.Since(dc.LastScan) < dc.Throttle {
			remaining := dc.Throttle - time.Since(dc.LastScan)
			return false, fmt.Sprintf("Gate 5: scan throttle active, %v remaining", remaining.Round(time.Second))
		}

		if dc.SessionCount < dc.MinSessions {
			return false, fmt.Sprintf("Gate 6: only %d sessions, need %d", dc.SessionCount, dc.MinSessions)
		}
	}

	// Try acquiring PID-based lock (Gate 7)
	locked, err := dc.acquireLock(saveDir)
	if err != nil || !locked {
		return false, "Gate 7: lock held by another process or failed to acquire lock"
	}

	return true, "All 7 gates passed"
}

// Consolidate executes the 4-phase consolidation routine.
func (dc *DreamConsolidator) Consolidate(mm *MemoryManager, force bool) ([]string, error) {
	saveDir, err := projectMemoryDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project memory directory: %w", err)
	}

	dc.LastScan = time.Now()

	// Gate Check
	canRun, reason := dc.ShouldConsolidate(mm, force)
	if !canRun {
		return nil, fmt.Errorf("consolidation blocked: %s", reason)
	}
	defer dc.releaseLock(saveDir)

	var completedPhases []string

	// Phase 1: Orient (Read MEMORY.md index and structure)
	completedPhases = append(completedPhases, "Phase 1: Orient - scan memory directory index")
	mm.Reload()

	// Phase 2: Gather (Load all memory entries and their contents)
	completedPhases = append(completedPhases, "Phase 2: Gather - read individual memory files for full content")
	mm.mu.RLock()
	var entries []*MemoryEntry
	for _, e := range mm.entries {
		entries = append(entries, e)
	}
	mm.mu.RUnlock()

	// Phase 3: Consolidate (Deduplicate identical content or merge duplicate names)
	completedPhases = append(completedPhases, "Phase 3: Consolidate - merge related memories and clean up duplicates")

	// Delete empty content or empty name entries
	var active []*MemoryEntry
	for _, e := range entries {
		if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Content) == "" {
			_ = mm.Delete(e.Name)
		} else {
			active = append(active, e)
		}
	}

	// Exact content deduplication within type groups
	seenContent := make(map[string]*MemoryEntry)
	var unique []*MemoryEntry
	for _, e := range active {
		normContent := strings.ToLower(strings.TrimSpace(e.Content))
		key := string(e.Type) + ":" + normContent
		if existing, found := seenContent[key]; found {
			// Duplicate content, delete the newer one
			toDelete := e
			if e.UpdatedAt.Before(existing.UpdatedAt) {
				toDelete = existing
				seenContent[key] = e
			}
			_ = mm.Delete(toDelete.Name)
			LogAudit(CatSession, "memory_consolidate_dedup", fmt.Sprintf("Removed duplicate memory '%s' in favor of '%s'", toDelete.Name, seenContent[key].Name), nil)
		} else {
			seenContent[key] = e
			unique = append(unique, e)
		}
	}

	// LLM-assisted Semantic Consolidation
	if globalLLMModel != nil {
		unique = dc.ConsolidateSemantically(mm, unique)
	}

	// Phase 4: Prune (Enforce maximum memory entries cap)
	completedPhases = append(completedPhases, "Phase 4: Prune - enforce max memory entries and rebuild index")

	// Sort by UpdatedAt ascending (oldest first)
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].UpdatedAt.Before(unique[j].UpdatedAt)
	})

	// If we exceed cap, prune oldest entries
	maxCap := MaxMemoryEntries
	if len(unique) > maxCap {
		pruneCount := len(unique) - maxCap
		for i := 0; i < pruneCount; i++ {
			_ = mm.Delete(unique[i].Name)
			LogAudit(CatSession, "memory_consolidate_prune", fmt.Sprintf("Pruned oldest memory entry '%s' to stay under cap", unique[i].Name), nil)
		}
	}

	// Rebuild index and save
	mm.Reload()
	dc.LastConsolidation = time.Now()
	return completedPhases, nil
}

// ConsolidateSemantically clusters and merges memories of the same type using the active LLM.
func (dc *DreamConsolidator) ConsolidateSemantically(mm *MemoryManager, entries []*MemoryEntry) []*MemoryEntry {
	if globalLLMModel == nil {
		return entries
	}

	// Group entries by type
	byType := make(map[MemoryType][]*MemoryEntry)
	for _, e := range entries {
		byType[e.Type] = append(byType[e.Type], e)
	}

	var consolidated []*MemoryEntry

	for mType, list := range byType {
		// Only run semantic merging if there are 3 or more entries of this type to keep it optimized
		if len(list) < 3 {
			consolidated = append(consolidated, list...)
			continue
		}

		LogAudit(CatSession, "memory_consolidate_semantic_start", fmt.Sprintf("Running semantic merging for %d memories of type %s", len(list), mType), nil)

		// Build consolidation prompt
		var builder strings.Builder
		for _, e := range list {
			builder.WriteString(fmt.Sprintf("- **%s**: %s\n  *Content*: %s\n\n", e.Name, e.Description, e.Content))
		}

		systemPrompt := fmt.Sprintf(`You are the persistent memory consolidation engine for Antigravity (a professional agent CLI).
Your task is to review a list of memories of type '%s' and merge any duplicate, redundant, or closely related guidelines into a single highly dense, compact representation.
You must output a valid JSON array of consolidated memory objects in EXACTLY this format:
[
  {
    "name": "durable_slug_name",
    "description": "Short description of the preference",
    "content": "Full, complete body of the preference. Do not use placeholders."
  }
]
Do not output any introductory or concluding text, and do not wrap in markdown blocks. Output only raw JSON.`, mType)

		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{
					Role: "user",
					Parts: []*genai.Part{
						{Text: systemPrompt + "\n\n[MEMORIES TO CONSOLIDATE]:\n" + builder.String()},
					},
				},
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var responseBuilder strings.Builder
		events := globalLLMModel.GenerateContent(ctx, req, false)
		for resp, err := range events {
			if err == nil && resp != nil && resp.Content != nil {
				for _, part := range resp.Content.Parts {
					if part.Text != "" {
						responseBuilder.WriteString(part.Text)
					}
				}
			}
		}
		cancel()

		responseText := strings.TrimSpace(responseBuilder.String())
		// Clean up markdown markers if the LLM returned them
		if strings.HasPrefix(responseText, "```") {
			lines := strings.Split(responseText, "\n")
			var clean []string
			for _, line := range lines {
				if !strings.HasPrefix(line, "```") {
					clean = append(clean, line)
				}
			}
			responseText = strings.Join(clean, "\n")
		}
		responseText = strings.TrimSpace(responseText)

		type SemanticItem struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Content     string `json:"content"`
		}

		var items []SemanticItem
		if err := json.Unmarshal([]byte(responseText), &items); err == nil && len(items) > 0 {
			// Delete the old entries from disk so they don't leak
			for _, e := range list {
				_ = mm.Delete(e.Name)
			}
			// Save the consolidated new entries
			for _, item := range items {
				slug := slugify(item.Name)
				if slug != "" && strings.TrimSpace(item.Content) != "" {
					_ = mm.Save(slug, item.Description, mType, item.Content)
					// Retrieve the saved entry to include in consolidated list
					mm.mu.RLock()
					if saved, ok := mm.entries[slug]; ok {
						consolidated = append(consolidated, saved)
					}
					mm.mu.RUnlock()
				}
			}
			LogAudit(CatSession, "memory_consolidate_semantic_success", fmt.Sprintf("Successfully consolidated %d entries down to %d", len(list), len(items)), nil)
		} else {
			// Fallback: keep original list intact
			consolidated = append(consolidated, list...)
			LogInfo(CatSession, "memory_consolidate_semantic_fallback", "Semantic consolidation failed or returned invalid JSON; falling back to original list", map[string]any{
				"error":    fmt.Sprintf("%v", err),
				"response": responseText,
			})
		}
	}

	return consolidated
}
