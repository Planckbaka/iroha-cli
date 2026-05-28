package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/tool"
)

// 1. file_read
type FileReadArgs struct {
	Path      string `json:"path" description:"The file path to read (relative or absolute)"`
	StartLine int    `json:"start_line,omitempty" description:"Line number to start reading from (1-based)"`
	EndLine   int    `json:"end_line,omitempty" description:"Line number to stop reading at (inclusive)"`
}

type FileReadResult struct {
	Content string `json:"content" description:"The file contents"`
}

const maxFileReadSize = 10 * 1024 * 1024 // 10MB

func FileReadHandler(ctx tool.Context, args FileReadArgs) (FileReadResult, error) {
	resolved := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolved); err != nil {
		return FileReadResult{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return FileReadResult{}, WrapToolError("file_read", args, fmt.Errorf("failed to read file: %w", err))
	}
	if info.IsDir() {
		return FileReadResult{}, fmt.Errorf("'%s' is a directory, not a file. Use shell_run with ls or find to explore directory structure", args.Path)
	}
	if info.Size() > maxFileReadSize {
		return FileReadResult{}, fmt.Errorf("file '%s' is %d bytes, exceeding the 10MB read limit. Use shell_run with head/tail to read in chunks", args.Path, info.Size())
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return FileReadResult{}, WrapToolError("file_read", args, fmt.Errorf("failed to read file: %w", err))
	}

	if args.StartLine == 0 && args.EndLine == 0 {
		return FileReadResult{Content: string(data)}, nil
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)

	start := args.StartLine
	if start < 1 {
		start = 1
	}
	end := args.EndLine
	if end <= 0 || end > total {
		end = total
	}
	if start > total {
		start = total
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Lines %d-%d of %d\n", start, end, total))
	for i := start - 1; i < end; i++ {
		b.WriteString(fmt.Sprintf("%d\t%s\n", i+1, lines[i]))
	}
	return FileReadResult{Content: b.String()}, nil
}

// 2. file_edit (exact string replacement)
type FileEditArgs struct {
	Path       string `json:"path" description:"The file path to edit"`
	OldString  string `json:"old_string" description:"Exact text to find and replace"`
	NewString  string `json:"new_string" description:"Replacement text"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences (default: first only)"`
	DryRun     bool   `json:"dry_run,omitempty" description:"Preview diff without writing"`
}

type FileEditResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Diff    string `json:"diff,omitempty"`
}

func FileEditHandler(ctx tool.Context, args FileEditArgs) (FileEditResult, error) {
	resolved := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolved); err != nil {
		return FileEditResult{}, err
	}

	if args.OldString == "" {
		return FileEditResult{}, fmt.Errorf("old_string must not be empty")
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return FileEditResult{}, WrapToolError("file_edit", args, fmt.Errorf("failed to read file: %w", err))
	}

	if len(data) > maxFileReadSize {
		return FileEditResult{}, fmt.Errorf("file '%s' exceeds 10MB edit limit", args.Path)
	}

	original := string(data)
	normalized := strings.ReplaceAll(original, "\r\n", "\n")

	var newContent string

	// Try exact match first
	idx := strings.Index(normalized, args.OldString)
	if idx != -1 {
		count := strings.Count(normalized, args.OldString)
		if !args.ReplaceAll && count > 1 {
			return FileEditResult{}, fmt.Errorf(
				"old_string matches %d times, provide more context or set replace_all=true", count)
		}
		if args.ReplaceAll {
			newContent = strings.ReplaceAll(normalized, args.OldString, args.NewString)
		} else {
			newContent = normalized[:idx] + args.NewString + normalized[idx+len(args.OldString):]
		}
	} else {
		// Fallback: line-based whitespace-tolerant match
		newContent, err = whitespaceTolerantEdit(normalized, args.OldString, args.NewString, args.ReplaceAll)
		if err != nil {
			return FileEditResult{}, WrapToolError("file_edit", args, fmt.Errorf(
				"old_string not found in file. %s", err.Error()))
		}
	}

	if newContent == normalized {
		return FileEditResult{}, fmt.Errorf("old_string not found in file — no changes made")
	}

	diff := generateUnifiedDiff(args.Path, original, newContent)

	if args.DryRun {
		return FileEditResult{
			Success: true,
			Message: "Dry run — no changes written",
			Diff:    diff,
		}, nil
	}

	snapshotFile(resolved)
	err = os.WriteFile(resolved, []byte(newContent), 0644)
	if err != nil {
		return FileEditResult{Success: false}, WrapToolError("file_edit", args, fmt.Errorf("failed to write file: %w", err))
	}

	return FileEditResult{
		Success: true,
		Message: "File edited successfully",
		Diff:    diff,
	}, nil
}

// normalizeLine trims trailing whitespace and collapses runs of spaces/tabs
// into a single space. Blank lines are preserved as empty strings.
func normalizeLine(line string) string {
	trimmed := strings.TrimRight(line, " \t")
	return strings.Join(strings.Fields(trimmed), " ")
}

// whitespaceTolerantEdit performs a line-based whitespace-tolerant replacement.
// It matches normalized lines against the normalized pattern lines, then
// replaces at the line level in the original content.
func whitespaceTolerantEdit(content, oldStr, newStr string, replaceAll bool) (string, error) {
	contentLines := strings.Split(content, "\n")
	patternLines := strings.Split(oldStr, "\n")

	matches := findLineMatches(contentLines, patternLines)
	if len(matches) == 0 {
		return "", fmt.Errorf("no match found even with whitespace tolerance")
	}

	if !replaceAll && len(matches) > 1 {
		return "", fmt.Errorf("old_string matches %d times (whitespace-tolerant), provide more context or set replace_all=true", len(matches))
	}

	newLines := strings.Split(newStr, "\n")
	result := make([]string, 0, len(contentLines))
	prevEnd := 0

	for _, match := range matches {
		start, end := match[0], match[1]
		result = append(result, contentLines[prevEnd:start]...)
		result = append(result, newLines...)
		prevEnd = end
	}
	result = append(result, contentLines[prevEnd:]...)

	return strings.Join(result, "\n"), nil
}

// findLineMatches returns all [start, end) line ranges where the normalized
// content lines match the normalized pattern lines.
func findLineMatches(contentLines, patternLines []string) [][2]int {
	normContent := make([]string, len(contentLines))
	for i, l := range contentLines {
		normContent[i] = normalizeLine(l)
	}
	normPattern := make([]string, len(patternLines))
	for i, l := range patternLines {
		normPattern[i] = normalizeLine(l)
	}

	var matches [][2]int
	plen := len(normPattern)
	for i := 0; i <= len(normContent)-plen; i++ {
		found := true
		for j := 0; j < plen; j++ {
			if normContent[i+j] != normPattern[j] {
				found = false
				break
			}
		}
		if found {
			matches = append(matches, [2]int{i, i + plen})
			if len(matches) >= 100 {
				break
			}
		}
	}
	return matches
}

func generateUnifiedDiff(path, oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	if len(oldLines) > 0 && oldLines[len(oldLines)-1] == "" {
		oldLines = oldLines[:len(oldLines)-1]
	}
	if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
		newLines = newLines[:len(newLines)-1]
	}

	ops := simpleDiff(oldLines, newLines)

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("--- %s\n+++ %s\n", path, path))

	// Group ops into hunks
	hunkStart := 0
	for hunkStart < len(ops) {
		// Find next change
		for hunkStart < len(ops) && ops[hunkStart].typ == "equal" {
			hunkStart++
		}
		if hunkStart >= len(ops) {
			break
		}

		// Find context start (3 lines back)
		ctxStart := hunkStart - 3
		if ctxStart < 0 {
			ctxStart = 0
		}

		// Find hunk end (next unchanged run)
		hunkEnd := hunkStart
		for hunkEnd < len(ops) && ops[hunkEnd].typ != "equal" {
			hunkEnd++
		}
		// Add trailing context
		ctxEnd := hunkEnd + 3
		if ctxEnd > len(ops) {
			ctxEnd = len(ops)
		}

		oldCount := 0
		newCount := 0
		for i := ctxStart; i < ctxEnd; i++ {
			switch ops[i].typ {
			case "equal":
				oldCount++
				newCount++
			case "delete":
				oldCount++
			case "insert":
				newCount++
			}
		}

		oldStart := 1
		newStart := 1
		for i := 0; i < ctxStart; i++ {
			if ops[i].typ == "equal" || ops[i].typ == "delete" {
				oldStart++
			}
			if ops[i].typ == "equal" || ops[i].typ == "insert" {
				newStart++
			}
		}

		buf.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount))
		for i := ctxStart; i < ctxEnd; i++ {
			switch ops[i].typ {
			case "equal":
				buf.WriteString(" " + ops[i].line + "\n")
			case "delete":
				buf.WriteString("-" + ops[i].line + "\n")
			case "insert":
				buf.WriteString("+" + ops[i].line + "\n")
			}
		}

		hunkStart = ctxEnd
	}

	return buf.String()
}

func simpleDiff(old, new []string) []struct {
	typ  string
	line string
} {
	m := len(old)
	n := len(new)

	// Build LCS table
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if old[i-1] == new[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to produce ops
	var result []struct {
		typ  string
		line string
	}
	i, j := m, n
	var ops []struct {
		typ  string
		line string
	}
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && old[i-1] == new[j-1] {
			ops = append(ops, struct {
				typ  string
				line string
			}{"equal", old[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, struct {
				typ  string
				line string
			}{"insert", new[j-1]})
			j--
		} else {
			ops = append(ops, struct {
				typ  string
				line string
			}{"delete", old[i-1]})
			i--
		}
	}

	for k := len(ops) - 1; k >= 0; k-- {
		result = append(result, ops[k])
	}
	return result
}

// 3. file_write (requires human confirmation)
type FileWriteArgs struct {
	Path    string `json:"path" description:"The file path to write to"`
	Content string `json:"content" description:"The text content to write"`
}

type FileWriteResult struct {
	Success bool `json:"success" description:"Whether the write succeeded"`
}

func FileWriteHandler(ctx tool.Context, args FileWriteArgs) (FileWriteResult, error) {
	resolved := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolved); err != nil {
		return FileWriteResult{Success: false}, err
	}
	// Create parent directories if they don't exist
	dir := filepath.Dir(resolved)
	if dir != "." && dir != "/" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return FileWriteResult{Success: false}, WrapToolError("file_write", args, fmt.Errorf("failed to create parent directory: %w", err))
		}
	}

	snapshotFile(resolved)
	err := os.WriteFile(resolved, []byte(args.Content), 0644)
	if err != nil {
		return FileWriteResult{Success: false}, WrapToolError("file_write", args, fmt.Errorf("failed to write file: %w", err))
	}
	return FileWriteResult{Success: true}, nil
}

// snapshotFile saves the original content of a file for rollback support.
func snapshotFile(absPath string) {
	pendingEditSnapshots.mu.Lock()
	defer pendingEditSnapshots.mu.Unlock()
	if _, exists := pendingEditSnapshots.snapshots[absPath]; exists {
		return
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		pendingEditSnapshots.snapshots[absPath] = ""
		return
	}
	pendingEditSnapshots.snapshots[absPath] = string(data)
}

func registerFileTools(r *ToolRegistry) {
	register(r, "file_read", "Read the contents of a file at the specified relative or absolute path.", FileReadHandler)
	register(r, "file_write", "Write the specified content to a file. This overwrites the file if it already exists.", FileWriteHandler)
	register(r, "file_edit", "Edit a file by replacing exact text matches. Supports single and replace-all modes with optional dry-run preview.", FileEditHandler)
	register(r, "file_edit_batch", "Apply multiple file edits atomically. All edits succeed or none are applied. Use this when making coordinated changes across multiple locations in one or more files.", FileEditBatchHandler)
	register(r, "search_grep", "Perform a global regex text search across the current directory, similar to grep/ripgrep.", GrepHandler)
	register(r, "find_files", "Find files matching a glob pattern. Supports ** for recursive matching. Excludes .git, node_modules, etc.", FindHandler)
	register(r, "list_directory", "List files and subdirectories under the specified directory. Supports recursive depth control and automatically skips large excluded directories like .git. This is the preferred tool for exploring project structure.", ListDirHandler)
}
