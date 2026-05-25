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
	Path string `json:"path" description:"要读取的文件路径（相对或绝对路径）"`
}

type FileReadResult struct {
	Content string `json:"content" description:"文件内容"`
}

const maxFileReadSize = 10 * 1024 * 1024 // 10MB

func FileReadHandler(ctx tool.Context, args FileReadArgs) (FileReadResult, error) {
	resolved := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolved); err != nil {
		return FileReadResult{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return FileReadResult{}, WrapToolError("file_read", args, fmt.Errorf("读取文件失败: %w", err))
	}
	if info.IsDir() {
		return FileReadResult{}, fmt.Errorf("'%s' 是一个目录，不是文件。请使用 shell_run 执行 ls 或 find 命令来查看目录结构", args.Path)
	}
	if info.Size() > maxFileReadSize {
		return FileReadResult{}, fmt.Errorf("文件 '%s' 大小为 %d 字节，超过 10MB 读取限制。请使用 shell_run 配合 head/tail 来分段读取", args.Path, info.Size())
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return FileReadResult{}, WrapToolError("file_read", args, fmt.Errorf("读取文件失败: %w", err))
	}
	return FileReadResult{Content: string(data)}, nil
}

// 2. file_write (需要人机确认)
type FileWriteArgs struct {
	Path    string `json:"path" description:"要写入的文件路径"`
	Content string `json:"content" description:"要写入的文本内容"`
}

type FileWriteResult struct {
	Success bool `json:"success" description:"是否写入成功"`
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
			return FileWriteResult{Success: false}, WrapToolError("file_write", args, fmt.Errorf("创建父目录失败: %w", err))
		}
	}

	err := os.WriteFile(resolved, []byte(args.Content), 0644)
	if err != nil {
		return FileWriteResult{Success: false}, WrapToolError("file_write", args, fmt.Errorf("写入文件失败: %w", err))
	}
	return FileWriteResult{Success: true}, nil
}

// 3. list_directory
type ListDirArgs struct {
	Path     string `json:"path" description:"要列出的目录路径（默认为当前工作目录）"`
	MaxDepth int    `json:"max_depth,omitempty" description:"递归深度（默认 1，仅当前层级；最大 4）"`
}

type ListDirResult struct {
	Entries []string `json:"entries" description:"目录条目列表（带 / 后缀表示子目录）"`
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
	Pattern string `json:"pattern" description:"正则表达式搜索模式"`
}

type GrepResult struct {
	Matches []string `json:"matches" description:"匹配到的行列表"`
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
		return GrepResult{}, fmt.Errorf("无效的正则表达式: %w", err)
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
