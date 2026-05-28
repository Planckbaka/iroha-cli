package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- Symlink escape tests ---

func TestValidateSandboxPath_SymlinkEscape(t *testing.T) {
	// Create a temp workspace dir
	workspace, err := os.MkdirTemp("", "iroha-sandbox-symlink-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)

	// Create a file outside the workspace
	outsideDir, err := os.MkdirTemp("", "iroha-sandbox-outside-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outsideDir)
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside workspace pointing to the outside file
	linkPath := filepath.Join(workspace, "link_to_secret")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), WorkdirKey, workspace)

	// Accessing the symlink should be blocked since it resolves outside the workspace
	err = validateSandboxPath(ctx, linkPath)
	if err == nil {
		t.Error("expected symlink escape to be blocked, but it passed validation")
	}
	if !strings.Contains(err.Error(), "security sandbox blocked") {
		t.Errorf("expected sandbox error, got: %v", err)
	}
}

func TestValidateSandboxPath_SymlinkWithinWorkspace(t *testing.T) {
	workspace, err := os.MkdirTemp("", "iroha-sandbox-symlinksafe-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)

	// Create a real file inside workspace
	realFile := filepath.Join(workspace, "real.txt")
	if err := os.WriteFile(realFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside workspace pointing to another file inside workspace
	linkPath := filepath.Join(workspace, "link_to_real")
	if err := os.Symlink(realFile, linkPath); err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), WorkdirKey, workspace)

	// Accessing an internal symlink should be allowed
	err = validateSandboxPath(ctx, linkPath)
	if err != nil {
		t.Errorf("expected internal symlink to be allowed, got error: %v", err)
	}
}

func TestValidateSandboxPath_SymlinkCWDOutsideTarget(t *testing.T) {
	// If the CWD itself is a symlink, we should still validate correctly
	outsideDir, err := os.MkdirTemp("", "iroha-sandbox-cwd-out-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outsideDir)

	// Create a symlink that points to the real workspace
	realWorkspace, err := os.MkdirTemp("", "iroha-sandbox-real-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(realWorkspace)

	linkWorkspace := filepath.Join(outsideDir, "workspace_link")
	if err := os.Symlink(realWorkspace, linkWorkspace); err != nil {
		t.Fatal(err)
	}

	// Use the symlinked path as CWD - file inside should still be allowed
	ctx := context.WithValue(context.Background(), WorkdirKey, linkWorkspace)
	targetPath := filepath.Join(linkWorkspace, "file.txt")

	err = validateSandboxPath(ctx, targetPath)
	if err != nil {
		t.Errorf("expected file inside symlinked CWD to be allowed, got: %v", err)
	}
}

// --- Environment variable expansion tests ---

func TestContainsEnvVarExpansion_Home(t *testing.T) {
	if !containsEnvVarExpansion("$HOME") {
		t.Error("expected $HOME to be detected as env var expansion")
	}
}

func TestContainsEnvVarExpansion_Braced(t *testing.T) {
	if !containsEnvVarExpansion("${HOME}") {
		t.Error("expected ${HOME} to be detected as env var expansion")
	}
}

func TestContainsEnvVarExpansion_PlainVar(t *testing.T) {
	if !containsEnvVarExpansion("$PATH") {
		t.Error("expected $PATH to be detected as env var expansion")
	}
}

func TestContainsEnvVarExpansion_UnderscorePrefix(t *testing.T) {
	if !containsEnvVarExpansion("$_MY_VAR") {
		t.Error("expected $_MY_VAR to be detected as env var expansion")
	}
}

func TestContainsEnvVarExpansion_EscapedDollar(t *testing.T) {
	if containsEnvVarExpansion("$$") {
		t.Error("expected $$ to NOT be detected as env var expansion (escaped dollar)")
	}
}

func TestContainsEnvVarExpansion_TrailingDollar(t *testing.T) {
	if containsEnvVarExpansion("price$") {
		t.Error("expected trailing $ with no identifier to NOT be detected as env var expansion")
	}
}

func TestContainsEnvVarExpansion_NoDollar(t *testing.T) {
	if containsEnvVarExpansion("/usr/bin/go") {
		t.Error("expected plain path to NOT be detected as env var expansion")
	}
}

func TestContainsEnvVarExpansion_DollarDigit(t *testing.T) {
	// $1, $2 etc are positional params in shell, not env vars
	if containsEnvVarExpansion("$1") {
		t.Error("expected $1 to NOT be detected as env var expansion")
	}
}

func TestCheckShellCommandSandbox_EnvVarBlocked(t *testing.T) {
	workspace, err := os.MkdirTemp("", "iroha-sandbox-envvar-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)

	ctx := context.WithValue(context.Background(), WorkdirKey, workspace)

	tests := []struct {
		name    string
		command string
	}{
		{"$HOME", "cat $HOME/.ssh/id_rsa"},
		{"${VAR}", "ls ${HOME}/secrets"},
		{"$VAR mid-token", "echo $MYVAR/foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkShellCommandSandbox(ctx, tt.command)
			if err == nil {
				t.Errorf("expected command %q to be blocked for env var expansion", tt.command)
			}
			if !strings.Contains(err.Error(), "environment variable") {
				t.Errorf("expected env var error, got: %v", err)
			}
		})
	}
}

// --- Relative path escape tests (comprehensive) ---

func TestCheckShellCommandSandbox_RelativePathEscape(t *testing.T) {
	workspace, err := os.MkdirTemp("", "iroha-sandbox-relative-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)

	ctx := context.WithValue(context.Background(), WorkdirKey, workspace)

	t.Run("../../etc/passwd escape", func(t *testing.T) {
		err := checkShellCommandSandbox(ctx, "cat ../../etc/passwd")
		if err == nil {
			t.Error("expected ../../etc/passwd to be blocked")
		}
		if !strings.Contains(err.Error(), "relative path escape") {
			t.Errorf("expected relative path escape error, got: %v", err)
		}
	})

	t.Run("../../../ escape", func(t *testing.T) {
		err := checkShellCommandSandbox(ctx, "ls ../../../")
		if err == nil {
			t.Error("expected ../../../ to be blocked")
		}
	})

	t.Run("valid relative path allowed", func(t *testing.T) {
		err := checkShellCommandSandbox(ctx, "cat src/main.go")
		if err != nil {
			t.Errorf("expected valid relative path to be allowed, got: %v", err)
		}
	})

	t.Run(".. inside workspace allowed", func(t *testing.T) {
		// If we're in /tmp/workspace/sub, then .. resolves to /tmp/workspace which is still inside
		subDir := filepath.Join(workspace, "sub")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		subCtx := context.WithValue(context.Background(), WorkdirKey, subDir)
		err := checkShellCommandSandbox(subCtx, "cat ../file.txt")
		if err != nil {
			t.Errorf("expected ../ within workspace to be allowed, got: %v", err)
		}
	})
}

func TestCheckShellCommandSandbox_AbsolutePathOutside(t *testing.T) {
	workspace, err := os.MkdirTemp("", "iroha-sandbox-abs-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)

	ctx := context.WithValue(context.Background(), WorkdirKey, workspace)

	t.Run("absolute path outside blocked", func(t *testing.T) {
		err := checkShellCommandSandbox(ctx, "cat /etc/shadow")
		if err == nil {
			t.Error("expected /etc/shadow to be blocked")
		}
		if !strings.Contains(err.Error(), "absolute path") {
			t.Errorf("expected absolute path error, got: %v", err)
		}
	})

	t.Run("safe prefix allowed", func(t *testing.T) {
		err := checkShellCommandSandbox(ctx, "ls /usr/bin")
		if err != nil {
			t.Errorf("expected /usr/bin to be allowed (safe prefix), got: %v", err)
		}
	})
}

func TestSandbox_Disabled(t *testing.T) {
	// Temporarily disable sandbox
	oldEnabled := GlobalSandboxEnabled
	GlobalSandboxEnabled = false
	defer func() { GlobalSandboxEnabled = oldEnabled }()

	cmd := exec.Command("echo", "hello")
	wrapped, err := WrapSandboxCommand(context.Background(), cmd, ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wrapped != cmd {
		t.Error("expected command to be returned unchanged when sandbox is disabled")
	}
}

func TestSandbox_MacProfileGeneration(t *testing.T) {
	workdir := "/tmp/my-workspace"
	profile := getMacSandboxProfile(workdir)

	// Validate key security properties
	if !strings.Contains(profile, `(deny file-write*`) {
		t.Error("expected profile to deny file writes")
	}
	if !strings.Contains(profile, `(subpath "/System")`) {
		t.Error("expected profile to protect system directories")
	}
	if !strings.Contains(profile, `(subpath "/tmp")`) {
		t.Error("expected profile to allow tmp directory writes")
	}
	if !strings.Contains(profile, `(subpath "/tmp/my-workspace")`) {
		t.Errorf("expected profile to allow write to workdir, got: %s", profile)
	}
}

func TestSandbox_PlatformWrapping(t *testing.T) {
	ctx := context.Background()
	cmd := exec.Command("echo", "hello")
	tempDir, err := os.MkdirTemp("", "iroha-sandbox-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	wrapped, err := WrapSandboxCommand(ctx, cmd, tempDir)
	if err != nil {
		t.Fatalf("unexpected error wrapping command: %v", err)
	}

	if runtime.GOOS == "darwin" {
		_, lookupErr := exec.LookPath("sandbox-exec")
		if lookupErr == nil {
			// Wrapped command should be sandbox-exec
			if !strings.Contains(wrapped.Path, "sandbox-exec") {
				t.Errorf("expected wrapped command path to contain sandbox-exec, got %s", wrapped.Path)
			}
			// Check args count
			if len(wrapped.Args) < 4 {
				t.Errorf("expected wrapped arguments, got %+v", wrapped.Args)
			}
		}
	} else if runtime.GOOS == "linux" {
		_, lookupErr := exec.LookPath("bwrap")
		if lookupErr == nil {
			if !strings.Contains(wrapped.Path, "bwrap") {
				t.Errorf("expected wrapped command path to contain bwrap, got %s", wrapped.Path)
			}
		}
	}
}

func TestSandbox_ExecutionRun(t *testing.T) {
	// Only run live execution tests on macOS/Linux where sandbox utility is present
	hasSandbox := false
	if runtime.GOOS == "darwin" {
		_, err := exec.LookPath("sandbox-exec")
		hasSandbox = (err == nil)
	} else if runtime.GOOS == "linux" {
		_, err := exec.LookPath("bwrap")
		hasSandbox = (err == nil)
	}

	if !hasSandbox {
		t.Skip("skipping live sandbox execution test because no sandbox utility (sandbox-exec/bwrap) is present")
	}

	tempDir, err := os.MkdirTemp("", "iroha-sandbox-run-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// We run a simple command that should succeed
	cmd := exec.Command("echo", "sandboxed execution works")
	wrapped, err := WrapSandboxCommand(context.Background(), cmd, tempDir)
	if err != nil {
		t.Fatalf("failed to wrap command: %v", err)
	}

	output, err := wrapped.CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed execution failed: %v, output: %s", err, string(output))
	}

	if !strings.Contains(string(output), "sandboxed execution works") {
		t.Errorf("expected output to contain 'sandboxed execution works', got: %s", string(output))
	}

	// Test write denial outside CWD
	var testDenyCmd *exec.Cmd
	denyFilePath := filepath.Join(os.TempDir(), "iroha_denied_write.txt")
	defer os.Remove(denyFilePath)

	if runtime.GOOS == "darwin" {
		// On macOS, try to write to a forbidden path (e.g. user home directory outside caches, or /etc)
		// We try writing to a file in a forbidden folder inside the profile
		// Since we allowed /tmp, let's write to a path we explicitly denied, e.g. a file directly in /private/etc (which is forbidden)
		testDenyCmd = exec.Command("touch", "/private/etc/iroha_test_touch")
	} else if runtime.GOOS == "linux" {
		// On Linux under bubblewrap, writing directly to root / is denied (since root is read-only)
		testDenyCmd = exec.Command("touch", "/iroha_test_touch")
	}

	if testDenyCmd != nil {
		wrappedDeny, err := WrapSandboxCommand(context.Background(), testDenyCmd, tempDir)
		if err != nil {
			t.Fatalf("failed to wrap write-denied command: %v", err)
		}

		// This command must fail due to permission denied
		err = wrappedDeny.Run()
		if err == nil {
			t.Error("expected file write command to fail due to sandbox permission restrictions, but it succeeded")
		}
	}
}
