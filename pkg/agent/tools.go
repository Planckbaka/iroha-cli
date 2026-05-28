package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ToolRegistry collects tools via table-driven registration.
type ToolRegistry struct {
	tools []tool.Tool
	err   error
}

// register creates a functiontool from config + handler and appends it.
func register[TArgs, TResults any](r *ToolRegistry, name, description string, handler functiontool.Func[TArgs, TResults]) {
	if r.err != nil {
		return
	}
	t, err := functiontool.New(functiontool.Config{
		Name:        name,
		Description: description,
	}, handler)
	if err != nil {
		r.err = fmt.Errorf("register tool %q: %w", name, err)
		return
	}
	r.tools = append(r.tools, t)
}

// lspConfigOnce guards one-time loading of user LSP server config.
var lspConfigOnce sync.Once

// WrapToolError enriches tool errors with actionable self-correction suggestions for the LLM
func WrapToolError(toolName string, args any, err error) error {
	if err == nil {
		return nil
	}

	errMsg := err.Error()

	// 1. Check for file not exist
	if errors.Is(err, os.ErrNotExist) || strings.Contains(errMsg, "no such file or directory") {
		return fmt.Errorf("%w\n[Self-repair suggestion] Please verify the file path is correct. If it may be a typo, use file_read/grep/search_grep to confirm the current directory structure or check if the target file exists.", err)
	}

	// 2. Check for permission issues
	if errors.Is(err, os.ErrPermission) || strings.Contains(errMsg, "permission denied") {
		return fmt.Errorf("%w\n[Self-repair suggestion] You do not have read/write permission for this path. Try writing to a different location within the workspace (current directory), or use shell_run to check permissions and directory attributes.", err)
	}

	// 3. Command execution failed
	if toolName == "shell_run" {
		return fmt.Errorf("%w\n[Self-repair suggestion] If the command failed due to a syntax error, incorrect arguments, or missing local dependencies, please verify your local development environment first. If a tool is missing, consider asking the user to authorize dependency installation, or use alternative Go commands to compile or test.", err)
	}

	return err
}

func getWorkdir(ctx context.Context) string {
	if ctx != nil {
		if val := ctx.Value(WorkdirKey); val != nil {
			if s, ok := val.(string); ok && s != "" {
				return s
			}
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}

func resolvePath(ctx context.Context, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	workdir := getWorkdir(ctx)
	return filepath.Join(workdir, path)
}

// validateSandboxPath checks if the resolved absolute path resides under the current working directory.
// It resolves symlinks on both the CWD and the target path to prevent symlink-based escapes.
func validateSandboxPath(ctx context.Context, rawPath string) error {
	cwd := getWorkdir(ctx)

	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return fmt.Errorf("invalid path format '%s': %w", rawPath, err)
	}

	// Resolve symlinks for both CWD and target to prevent symlink-based escape.
	// filepath.EvalSymlinks resolves all symlinks in the path.
	evalCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		// If the CWD can't be resolved (e.g. deleted), fall back to cleaned path
		evalCWD = filepath.Clean(cwd)
	}
	evalPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// If the target doesn't exist or can't be resolved, fall back to cleaned path
		evalPath = filepath.Clean(absPath)
	}

	cleanCWD := filepath.Clean(evalCWD)
	cleanAbs := filepath.Clean(evalPath)

	if cleanAbs != cleanCWD && !strings.HasPrefix(cleanAbs, cleanCWD+string(os.PathSeparator)) {
		return fmt.Errorf("security sandbox blocked: path '%s' is outside the workspace root '%s'", rawPath, cleanCWD)
	}
	return nil
}

// checkShellCommandSandbox scans a shell command for relative path escaping, out-of-bounds absolute
// path accesses, and environment variable expansion attempts.
func checkShellCommandSandbox(ctx context.Context, command string) error {
	cwd := getWorkdir(ctx)
	cleanCWD := filepath.Clean(cwd)

	tokens, err := tokenizeCommand(command)
	if err != nil {
		return err
	}

	for _, w := range tokens {
		// 1. Detect relative escaping
		if strings.Contains(w, "..") {
			abs, err := filepath.Abs(w)
			if err == nil {
				if !strings.HasPrefix(filepath.Clean(abs), cleanCWD) {
					return fmt.Errorf("security sandbox blocked: detected out-of-bounds relative path escape '%s' in command", w)
				}
			}
		}

		// 2. Detect absolute paths outside CWD
		if strings.HasPrefix(w, "/") {
			isSystemSafe := false
			for _, prefix := range safePrefixes {
				if strings.HasPrefix(w, prefix) {
					isSystemSafe = true
					break
				}
			}
			if !isSystemSafe {
				cleanPath := filepath.Clean(w)
				if !strings.HasPrefix(cleanPath, cleanCWD) {
					return fmt.Errorf("security sandbox blocked: detected attempt to access absolute path '%s' outside the workspace in command", w)
				}
			}
		}

		// 3. Detect environment variable expansion ($HOME, ${VAR}, $VAR)
		if containsEnvVarExpansion(w) {
			return fmt.Errorf("security sandbox blocked: detected environment variable expansion '%s' in command — use explicit paths instead", w)
		}
	}
	return nil
}

// containsEnvVarExpansion checks if a token contains shell environment variable patterns
// like $HOME, ${VAR}, or $VAR that would be expanded at execution time.
func containsEnvVarExpansion(s string) bool {
	n := len(s)
	for i := 0; i < n; i++ {
		if s[i] != '$' {
			continue
		}
		if i+1 < n && s[i+1] == '$' {
			i++
			continue
		}
		if i+1 >= n {
			continue
		}
		if s[i+1] == '{' {
			return true
		}
		if s[i+1] == '_' || (s[i+1] >= 'a' && s[i+1] <= 'z') || (s[i+1] >= 'A' && s[i+1] <= 'Z') {
			return true
		}
	}
	return false
}

// GetSWETools returns the list of SWE tools for the Agent.
func GetSWETools() ([]tool.Tool, error) {
	var r ToolRegistry

	registerFileTools(&r)
	registerShellTools(&r)
	registerTodoTools(&r)
	registerMemoryTools(&r)
	registerTaskTools(&r)
	registerScheduleTools(&r)
	registerTeamTools(&r)
	registerWorktreeTools(&r)
	registerMCPTools(&r)
	registerCITools(&r)
	registerWebTools(&r)
	registerSubagentTools(&r)
	registerLSPTools(&r)

	if r.err != nil {
		return nil, r.err
	}

	// Dynamic MCP plugin tools
	_ = GlobalMCPRouter.LoadAndStartPlugins()
	if mcpTools, err := GlobalMCPRouter.DiscoverTools(); err == nil {
		r.tools = append(r.tools, mcpTools...)
	}

	return r.tools, nil
}
