package agent

import (
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/tool"
)

// 6. file_edit_batch (atomic multi-edit with rollback)
type FileEditBatchArgs struct {
	Edits []FileEditArgs `json:"edits" description:"List of edits to apply atomically"`
}

type FileEditBatchResult struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Diffs   []string `json:"diffs"`
}

func FileEditBatchHandler(ctx tool.Context, args FileEditBatchArgs) (FileEditBatchResult, error) {
	if len(args.Edits) == 0 {
		return FileEditBatchResult{}, fmt.Errorf("edits list must not be empty")
	}
	if len(args.Edits) > 50 {
		return FileEditBatchResult{}, fmt.Errorf("batch edit limited to 50 edits, got %d", len(args.Edits))
	}

	// Phase 1: Validate all edits — read files and verify old_string exists
	type editPlan struct {
		resolvedPath string
		edit         FileEditArgs
		original     string
		newContent   string
		diff         string
	}
	plans := make([]editPlan, 0, len(args.Edits))

	for i, edit := range args.Edits {
		resolved := resolvePath(ctx, edit.Path)
		if err := validateSandboxPath(ctx, resolved); err != nil {
			rollbackPendingEdits()
			return FileEditBatchResult{}, fmt.Errorf("edit %d: %w", i, err)
		}

		if edit.OldString == "" {
			rollbackPendingEdits()
			return FileEditBatchResult{}, fmt.Errorf("edit %d: old_string must not be empty", i)
		}

		data, err := os.ReadFile(resolved)
		if err != nil {
			rollbackPendingEdits()
			return FileEditBatchResult{}, fmt.Errorf("edit %d: %w", i, WrapToolError("file_edit_batch", edit, fmt.Errorf("failed to read file: %w", err)))
		}

		if len(data) > maxFileReadSize {
			rollbackPendingEdits()
			return FileEditBatchResult{}, fmt.Errorf("edit %d: file '%s' exceeds 10MB edit limit", i, edit.Path)
		}

		normalized := strings.ReplaceAll(string(data), "\r\n", "\n")

		var newContent string
		idx := strings.Index(normalized, edit.OldString)
		if idx != -1 {
			count := strings.Count(normalized, edit.OldString)
			if !edit.ReplaceAll && count > 1 {
				rollbackPendingEdits()
				return FileEditBatchResult{}, fmt.Errorf("edit %d: old_string matches %d times, provide more context or set replace_all=true", i, count)
			}
			if edit.ReplaceAll {
				newContent = strings.ReplaceAll(normalized, edit.OldString, edit.NewString)
			} else {
				newContent = normalized[:idx] + edit.NewString + normalized[idx+len(edit.OldString):]
			}
		} else {
			// Fallback: whitespace-tolerant match
			var wsErr error
			newContent, wsErr = whitespaceTolerantEdit(normalized, edit.OldString, edit.NewString, edit.ReplaceAll)
			if wsErr != nil {
				rollbackPendingEdits()
				return FileEditBatchResult{}, fmt.Errorf("edit %d: %w", i, WrapToolError("file_edit_batch", edit, wsErr))
			}
		}

		if newContent == normalized {
			rollbackPendingEdits()
			return FileEditBatchResult{}, fmt.Errorf("edit %d: old_string not found in file — no changes made", i)
		}

		diff := generateUnifiedDiff(edit.Path, string(data), newContent)
		plans = append(plans, editPlan{
			resolvedPath: resolved,
			edit:         edit,
			original:     string(data),
			newContent:   newContent,
			diff:         diff,
		})
	}

	// Phase 2: Snapshot all files and apply all edits
	diffs := make([]string, 0, len(plans))
	for i, plan := range plans {
		snapshotFile(plan.resolvedPath)
		if err := os.WriteFile(plan.resolvedPath, []byte(plan.newContent), 0644); err != nil {
			// Rollback everything applied so far
			rollbackPendingEdits()
			return FileEditBatchResult{
				Success: false,
				Message: fmt.Sprintf("edit %d failed to write, all changes rolled back: %v", i, err),
			}, err
		}
		diffs = append(diffs, plan.diff)
	}

	return FileEditBatchResult{
		Success: true,
		Message: fmt.Sprintf("Successfully applied %d edits atomically", len(plans)),
		Diffs:   diffs,
	}, nil
}
