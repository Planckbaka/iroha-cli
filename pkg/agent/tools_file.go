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
	Path string `json:"path" description:"The file path to read (relative or absolute)"`
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
	return FileReadResult{Content: string(data)}, nil
}

// 2. file_write (需要人机确认)
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
