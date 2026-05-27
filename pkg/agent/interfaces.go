package agent

import (
	"context"

	"google.golang.org/adk/session"
)

// MemoryStore defines the interface for persistent memory operations.
// *MemoryManager satisfies this interface implicitly.
type MemoryStore interface {
	Save(name, description string, memType MemoryType, content string) error
	List() map[MemoryType][]*MemoryEntry
	Search(query string) []*MemoryEntry
	Update(name, description string, memType MemoryType, content string) error
	Delete(name string) error
	Count() int
	BuildSystemPromptSection(currentPrompt ...string) string
}

// SessionStore defines the interface for session persistence operations.
// *PersistentSessionService satisfies this interface implicitly.
type SessionStore interface {
	LoadSessions(ctx context.Context) error
	Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error)
	Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error)
}

// PermissionChecker defines the interface for permission checking operations.
// *PermissionManager satisfies this interface implicitly.
type PermissionChecker interface {
	Check(toolName string, args any) (decision string, reason string)
	GetMode() PermissionMode
	NoteApproval()
	NoteDenial() int
}

// Compile-time interface satisfaction checks.
var _ MemoryStore = (*MemoryManager)(nil)
var _ SessionStore = (*PersistentSessionService)(nil)
var _ PermissionChecker = (*PermissionManager)(nil)
