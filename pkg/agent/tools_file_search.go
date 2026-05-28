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

	if err != nil {
		return GrepResult{}, WrapToolError("search_grep", args, err)
	}
	return GrepResult{Matches: matches}, nil
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
