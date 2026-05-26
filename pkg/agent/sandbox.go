package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GlobalSandboxEnabled controls whether physical OS-level sandboxing is applied to executed shell commands.
var GlobalSandboxEnabled = true

// WrapSandboxCommand inspects the host OS and wraps an exec.Cmd in a physical sandbox jail.
func WrapSandboxCommand(ctx context.Context, cmd *exec.Cmd, workdir string) (*exec.Cmd, error) {
	if !GlobalSandboxEnabled {
		return cmd, nil
	}

	absWorkdir, err := filepath.Abs(workdir)
	if err != nil {
		absWorkdir = workdir
	}

	switch runtime.GOOS {
	case "darwin":
		return wrapMacSandbox(ctx, cmd, absWorkdir)
	case "linux":
		return wrapLinuxSandbox(ctx, cmd, absWorkdir)
	default:
		// Fallback for Windows and other systems
		return cmd, nil
	}
}

func wrapMacSandbox(ctx context.Context, cmd *exec.Cmd, workdir string) (*exec.Cmd, error) {
	sandboxExecPath, err := exec.LookPath("sandbox-exec")
	if err != nil {
		// If sandbox-exec is missing, fall back gracefully
		return cmd, nil
	}

	profile := getMacSandboxProfile(workdir)

	// sandbox-exec takes -p <profile> and then command + args
	originalPath := cmd.Path
	originalArgs := cmd.Args

	// The wrapped command will run sandbox-exec -p <profile> <original_command> <original_args...>
	// Note: cmd.Args[0] is the command itself in exec.Cmd
	newArgs := []string{"sandbox-exec", "-p", profile}
	if len(originalArgs) > 0 {
		newArgs = append(newArgs, originalArgs...)
	} else {
		newArgs = append(newArgs, originalPath)
	}

	wrappedCmd := exec.CommandContext(ctx, sandboxExecPath, newArgs[1:]...)
	wrappedCmd.Dir = cmd.Dir
	wrappedCmd.Env = cmd.Env
	wrappedCmd.Stdout = cmd.Stdout
	wrappedCmd.Stderr = cmd.Stderr
	wrappedCmd.Stdin = cmd.Stdin

	return wrappedCmd, nil
}

func getMacSandboxProfile(workdir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	var sb strings.Builder
	sb.WriteString("(version 1)\n(allow default)\n")

	// 1. Deny file writes to sensitive OS directories
	sb.WriteString("(deny file-write*\n")
	sb.WriteString("  (subpath \"/System\")\n")
	sb.WriteString("  (subpath \"/Library\")\n")
	sb.WriteString("  (subpath \"/usr\")\n")
	sb.WriteString("  (subpath \"/bin\")\n")
	sb.WriteString("  (subpath \"/sbin\")\n")
	sb.WriteString("  (subpath \"/private/etc\")\n")

	// Protect high-value credentials and SSH directories
	if home != "" {
		sb.WriteString(fmt.Sprintf("  (subpath \"%s/.ssh\")\n", home))
		sb.WriteString(fmt.Sprintf("  (subpath \"%s/.aws\")\n", home))
		sb.WriteString(fmt.Sprintf("  (subpath \"%s/.kube\")\n", home))
		sb.WriteString(fmt.Sprintf("  (subpath \"%s/.gemini\")\n", home))
	}
	sb.WriteString(")\n")

	// 2. Explicitly permit file writes to target workspace, caches, and tmp directories
	sb.WriteString("(allow file-write*\n")
	sb.WriteString(fmt.Sprintf("  (subpath \"%s\")\n", workdir))
	sb.WriteString("  (subpath \"/private/tmp\")\n")
	sb.WriteString("  (subpath \"/tmp\")\n")
	sb.WriteString("  (subpath \"/private/var/folders\")\n")
	sb.WriteString("  (subpath \"/var/folders\")\n")

	if home != "" {
		sb.WriteString(fmt.Sprintf("  (subpath \"%s/Library/Caches\")\n", home))
		sb.WriteString(fmt.Sprintf("  (subpath \"%s/.cache\")\n", home))
		// Allow go-build cache writes
		sb.WriteString(fmt.Sprintf("  (subpath \"%s/Library/Application Support\")\n", home))
	}
	sb.WriteString(")\n")

	return sb.String()
}

func wrapLinuxSandbox(ctx context.Context, cmd *exec.Cmd, workdir string) (*exec.Cmd, error) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		// Bubblewrap is not installed, fall back gracefully
		return cmd, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	// Build Bubblewrap container arguments
	// --ro-bind / /  -> read-only mount root filesystem
	// --dev /dev     -> mount /dev
	// --proc /proc   -> mount /proc
	// --tmpfs /tmp   -> sandboxed /tmp
	// --bind <workdir> <workdir> -> allow write access to workspace
	bwrapArgs := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--bind", workdir, workdir,
	}

	// Bind compilation cache and user temp dirs to prevent compiler failure
	if home != "" {
		cacheDir := filepath.Join(home, ".cache")
		_ = os.MkdirAll(cacheDir, 0755)
		bwrapArgs = append(bwrapArgs, "--bind", cacheDir, cacheDir)
	}

	originalPath := cmd.Path
	originalArgs := cmd.Args

	// Append command
	bwrapArgs = append(bwrapArgs, originalPath)
	if len(originalArgs) > 1 {
		bwrapArgs = append(bwrapArgs, originalArgs[1:]...)
	}

	wrappedCmd := exec.CommandContext(ctx, bwrapPath, bwrapArgs...)
	wrappedCmd.Dir = cmd.Dir
	wrappedCmd.Env = cmd.Env
	wrappedCmd.Stdout = cmd.Stdout
	wrappedCmd.Stderr = cmd.Stderr
	wrappedCmd.Stdin = cmd.Stdin

	return wrappedCmd, nil
}
