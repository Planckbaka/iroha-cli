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
