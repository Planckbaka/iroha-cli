package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// 3. list_directory
type ListDirArgs struct {
	Path     string `json:"path" description:"The directory path to list (defaults to current working directory)"`
	MaxDepth int    `json:"max_depth,omitempty" description:"Recursive depth (default 1, current level only; max 4)"`
}

type ListDirResult struct {
	Entries []string `json:"entries" description:"List of directory entries (subdirectories have / suffix)"`
}

func ListDirHandler(ctx tool.Context, args ListDirArgs) (ListDirResult, error) {
	if args.Path == "" {
		args.Path = "."
	}
	resolved := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolved); err != nil {
		return ListDirResult{}, err
	}
	if args.MaxDepth <= 0 {
		args.MaxDepth = 1
	}
	if args.MaxDepth > 4 {
		args.MaxDepth = 4
	}

	root := resolved
	var entries []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip excluded directories
		if info.IsDir() && grepExcludedDirs[info.Name()] {
			return filepath.SkipDir
		}

		// Calculate current depth
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if rel == "." {
			return nil // skip root itself
		}

		depth := len(strings.Split(rel, string(filepath.Separator)))
		if depth > args.MaxDepth {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			entries = append(entries, rel+"/")
		} else {
			entries = append(entries, rel)
		}

		if len(entries) >= 200 {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return ListDirResult{}, WrapToolError("list_directory", args, err)
	}

	return ListDirResult{Entries: entries}, nil
}

// 4. search_grep
type GrepArgs struct {
	Pattern string `json:"pattern" description:"The regex search pattern"`
}

type GrepResult struct {
	Matches []string `json:"matches" description:"List of matched lines"`
}

var grepExcludedDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true,
	"vendor": true, "__pycache__": true, ".next": true,
	"dist": true, "build": true, ".cache": true,
}

const maxGrepFileSize = 1 * 1024 * 1024 // 1MB

func GrepHandler(ctx tool.Context, args GrepArgs) (GrepResult, error) {
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return GrepResult{}, fmt.Errorf("invalid regex pattern: %w", err)
	}

	var matches []string
	cwd := getWorkdir(ctx)

	err = filepath.Walk(cwd, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip excluded directories entirely — must return SkipDir, not nil
		if info.IsDir() {
			if grepExcludedDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip large files
		if info.Size() > maxGrepFileSize {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(cwd, path)
		lines := bytes.Split(data, []byte("\n"))
		for i, line := range lines {
			if re.Match(line) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, i+1, string(line)))
				if len(matches) >= 50 {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	return GrepResult{Matches: matches}, err
}

// 5. find_files
type FindArgs struct {
	Pattern string `json:"pattern" description:"Glob pattern (e.g. '**/*.go', 'src/**/*.ts')"`
	Path    string `json:"path,omitempty" description:"Directory to search in (default: CWD)"`
}

type FindResult struct {
	Files []string `json:"files" description:"Matching file paths (relative)"`
	Total int      `json:"total" description:"Total matches found"`
}

func FindHandler(ctx tool.Context, args FindArgs) (FindResult, error) {
	searchDir := args.Path
	if searchDir == "" {
		searchDir = "."
	}
	resolved := resolvePath(ctx, searchDir)
	if err := validateSandboxPath(ctx, resolved); err != nil {
		return FindResult{}, err
	}

	var files []string
	err := filepath.WalkDir(resolved, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if grepExcludedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(resolved, path)
		if relErr != nil {
			return nil
		}

		if matchGlob(args.Pattern, rel) {
			files = append(files, rel)
			if len(files) >= 100 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return FindResult{}, WrapToolError("find_files", args, err)
	}

	sortFiles(files)
	return FindResult{Files: files, Total: len(files)}, nil
}

func matchGlob(pattern, path string) bool {
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")
	return matchGlobParts(patternParts, pathParts)
}

func matchGlobParts(patternParts, pathParts []string) bool {
	for len(patternParts) > 0 {
		seg := patternParts[0]

		if seg == "**" {
			patternParts = patternParts[1:]

			// ** matches zero or more path segments
			// Try matching remaining pattern at every position
			for i := 0; i <= len(pathParts); i++ {
				if matchGlobParts(patternParts, pathParts[i:]) {
					return true
				}
			}
			return false
		}

		if len(pathParts) == 0 {
			return false
		}

		matched, err := filepath.Match(seg, pathParts[0])
		if err != nil || !matched {
			return false
		}

		patternParts = patternParts[1:]
		pathParts = pathParts[1:]
	}

	return len(pathParts) == 0
}

func sortFiles(files []string) {
	for i := 0; i < len(files)-1; i++ {
		for j := i + 1; j < len(files); j++ {
			if files[i] > files[j] {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
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
