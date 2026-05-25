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
