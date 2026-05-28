package agent

import (
	"google.golang.org/adk/tool"
)

// MemorySaveArgs mirrors the four fields a memory entry needs.
type MemorySaveArgs struct {
	Name        string `json:"name"        description:"Unique identifier for the memory entry (English, underscore-separated)"`
	Description string `json:"description" description:"One-line summary for indexing and system prompt display"`
	Type        string `json:"type"        description:"Memory type: user (user preferences), feedback (corrections), project (project facts), reference (external resource pointers)"`
	Content     string `json:"content"     description:"Detailed body content of the memory"`
}

type MemorySaveResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func MemorySaveHandler(_ tool.Context, args MemorySaveArgs) (MemorySaveResult, error) {
	err := GlobalMemoryManager.Save(args.Name, args.Description, MemoryType(args.Type), args.Content)
	if err != nil {
		return MemorySaveResult{OK: false, Message: err.Error()}, nil
	}
	return MemorySaveResult{OK: true, Message: "Memory saved: " + args.Name}, nil
}

// MemoryListArgs — no parameters needed, lists everything.
type MemoryListArgs struct{}

type MemoryListResult struct {
	Total   int                        `json:"total"`
	Entries map[string][]MemoryListRow `json:"entries"`
}

type MemoryListRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func MemoryListHandler(_ tool.Context, _ MemoryListArgs) (MemoryListResult, error) {
	all := GlobalMemoryManager.List()
	out := MemoryListResult{
		Entries: make(map[string][]MemoryListRow),
	}
	for t, entries := range all {
		for _, e := range entries {
			out.Entries[string(t)] = append(out.Entries[string(t)], MemoryListRow{
				Name:        e.Name,
				Description: e.Description,
			})
			out.Total++
		}
	}
	return out, nil
}

// MemorySearchArgs holds the query for searching memories.
type MemorySearchArgs struct {
	Query string `json:"query" description:"Search query to find matching memories"`
}

// MemorySearchResult holds search results.
type MemorySearchResult struct {
	Total   int             `json:"total"`
	Results []MemoryListRow `json:"results"`
}

func MemorySearchHandler(_ tool.Context, args MemorySearchArgs) (MemorySearchResult, error) {
	matches := GlobalMemoryManager.Search(args.Query)
	result := MemorySearchResult{
		Total:   len(matches),
		Results: make([]MemoryListRow, 0, len(matches)),
	}
	for _, e := range matches {
		result.Results = append(result.Results, MemoryListRow{
			Name:        e.Name,
			Description: e.Description,
		})
	}
	return result, nil
}

// MemoryUpdateArgs holds arguments for updating a memory entry.
type MemoryUpdateArgs struct {
	Name        string `json:"name"        description:"Name of the existing memory entry to update"`
	Description string `json:"description" description:"Updated one-line summary"`
	Type        string `json:"type"        description:"Memory type: user, feedback, project, reference"`
	Content     string `json:"content"     description:"Updated body content"`
}

// MemoryUpdateResult holds the result of an update operation.
type MemoryUpdateResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func MemoryUpdateHandler(_ tool.Context, args MemoryUpdateArgs) (MemoryUpdateResult, error) {
	err := GlobalMemoryManager.Update(args.Name, args.Description, MemoryType(args.Type), args.Content)
	if err != nil {
		return MemoryUpdateResult{OK: false, Message: err.Error()}, nil
	}
	return MemoryUpdateResult{OK: true, Message: "Memory updated: " + args.Name}, nil
}

// MemoryDeleteArgs holds arguments for deleting a memory entry.
type MemoryDeleteArgs struct {
	Name string `json:"name" description:"Name of the memory entry to delete"`
}

// MemoryDeleteResult holds the result of a delete operation.
type MemoryDeleteResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func MemoryDeleteHandler(_ tool.Context, args MemoryDeleteArgs) (MemoryDeleteResult, error) {
	err := GlobalMemoryManager.Delete(args.Name)
	if err != nil {
		return MemoryDeleteResult{OK: false, Message: err.Error()}, nil
	}
	return MemoryDeleteResult{OK: true, Message: "Memory deleted: " + args.Name}, nil
}

// MemoryDreamArgs holds arguments to manually trigger memory consolidation.
type MemoryDreamArgs struct {
	Force bool `json:"force" description:"If true, bypasses cooldown, scan throttle, and session thresholds (Gates 4, 5, 6)"`
}

// MemoryDreamResult holds the phases executed or error.
type MemoryDreamResult struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Phases  []string `json:"phases,omitempty"`
}

func MemoryDreamHandler(_ tool.Context, args MemoryDreamArgs) (MemoryDreamResult, error) {
	phases, err := GlobalDreamConsolidator.Consolidate(GlobalMemoryManager, args.Force)
	if err != nil {
		return MemoryDreamResult{OK: false, Message: err.Error()}, nil
	}
	return MemoryDreamResult{
		OK:      true,
		Message: "Dream consolidation completed successfully.",
		Phases:  phases,
	}, nil
}

func registerMemoryTools(r *ToolRegistry) {
	register(r,"memory_save", "Save a persistent memory entry to disk that survives across sessions. Use this for user preferences, feedback corrections, project constraints, external resource pointers, or other critical information that cannot be re-derived from the codebase. Do not use for current task state, temporary branch names, secrets, or anything directly readable from the repository.", MemorySaveHandler)
	register(r,"memory_list", "List all currently loaded persistent memory entries in the current session, grouped by type (user/feedback/project/reference).", MemoryListHandler)
	register(r,"memory_search", "Search persistent memory entries by keyword query (case-insensitive). Returns matching entries sorted by relevance.", MemorySearchHandler)
	register(r,"memory_update", "Update an existing persistent memory entry by name. Modifies the description, type, and content fields.", MemoryUpdateHandler)
	register(r,"memory_delete", "Delete a persistent memory entry by name. Removes it from disk and the in-memory store.", MemoryDeleteHandler)
	register(r,"memory_dream", "Manually trigger the 4-phase persistent memory consolidation ('Dream') pass to deduplicate, merge, and prune stored memory entries.", MemoryDreamHandler)
}
