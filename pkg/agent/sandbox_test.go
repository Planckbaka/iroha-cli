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
