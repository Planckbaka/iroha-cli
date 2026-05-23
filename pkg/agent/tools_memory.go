package agent

import (
	"google.golang.org/adk/tool"
)

// MemorySaveArgs mirrors the four fields a memory entry needs.
type MemorySaveArgs struct {
	Name        string `json:"name"        description:"记忆条目的唯一标识名称（英文、下划线分隔）"`
	Description string `json:"description" description:"一行简短描述，用于索引和系统提示中展示"`
	Type        string `json:"type"        description:"记忆类型：user（用户偏好）、feedback（反馈更正）、project（项目事实）、reference（外部资源指针）"`
	Content     string `json:"content"     description:"记忆的详细正文内容"`
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
	return MemorySaveResult{OK: true, Message: "✅ 记忆已保存: " + args.Name}, nil
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
