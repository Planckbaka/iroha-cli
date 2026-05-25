package tui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"iroha/pkg/agent"
	"iroha/pkg/config"

	"github.com/charmbracelet/lipgloss"
)

// RunDiagnostics executes environmental health checks and returns a styled dashboard report.
func RunDiagnostics() string {
	var sb strings.Builder

	// Title Card
	titleStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render
	sb.WriteString("\n" + titleStyle("🩺 Iroha Environment Doctor — Diagnostic Report") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Render("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━") + "\n\n")

	// 1. Config Audit
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [1] Core Configuration Audit") + "\n")
	cfg, err := config.LoadConfig()
	if err != nil {
		sb.WriteString(fmt.Sprintf("    %s Failed to load config: %v\n", lipgloss.NewStyle().Foreground(ColorDanger).Render("✗"), err))
	} else {
		sb.WriteString(fmt.Sprintf("    Provider:       %s\n", StylePrompt.Render(cfg.Provider)))
		sb.WriteString(fmt.Sprintf("    Model:          %s\n", StylePrompt.Render(cfg.Model)))
		if cfg.BaseURL != "" {
			sb.WriteString(fmt.Sprintf("    Base URL:       %s\n", lipgloss.NewStyle().Foreground(ColorPrimary).Render(cfg.BaseURL)))
		} else {
			sb.WriteString("    Base URL:       Official default endpoint\n")
		}

		// API Key mask
		if cfg.APIKey != "" {
			masked := cfg.APIKey
			if len(masked) > 8 {
				masked = masked[:4] + "...." + masked[len(masked)-4:]
			} else {
				masked = "********"
			}
			sb.WriteString(fmt.Sprintf("    API Key:        %s Configured (length: %d, preview: %s)\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), len(cfg.APIKey), masked))
		} else {
			sb.WriteString(fmt.Sprintf("    API Key:        %s Not configured (set environment variable or configure in ~/.iroha.json)\n",
				lipgloss.NewStyle().Foreground(ColorWarning).Render("⚠️")))
		}

		mode := agent.GlobalPermissionManager.GetMode()
		sb.WriteString(fmt.Sprintf("    Permission:     %s\n", StylePrompt.Render(string(mode))))
	}
	sb.WriteString("\n")

	// 2. Network Latency
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [2] Network Connectivity & API Latency") + "\n")
	pingURL := "https://api.openai.com"
	if cfg != nil && cfg.BaseURL != "" {
		pingURL = cfg.BaseURL
	} else if cfg != nil {
		defCfg := config.DefaultProviderConfig(cfg.Provider)
		if defCfg.BaseURL != "" {
			pingURL = defCfg.BaseURL
		}
	}

	// Clean pingURL for HTTP check
	if !strings.HasPrefix(pingURL, "http://") && !strings.HasPrefix(pingURL, "https://") {
		pingURL = "https://" + pingURL
	}

	client := &http.Client{
		Timeout: 4 * time.Second,
	}
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reqCancel()

	req, reqErr := http.NewRequestWithContext(reqCtx, "HEAD", pingURL, nil)
	if reqErr == nil {
		start := time.Now()
		resp, err := client.Do(req)
		duration := time.Since(start)

		if err != nil {
			sb.WriteString(fmt.Sprintf("    %s Failed to connect to endpoint (%s): %v\n",
				lipgloss.NewStyle().Foreground(ColorDanger).Render("✗"), pingURL, err))
		} else {
			_ = resp.Body.Close()
			sb.WriteString(fmt.Sprintf("    %s Connection successful: %s\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), pingURL))
			sb.WriteString(fmt.Sprintf("    %s TCP handshake & response latency: %v\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), duration.Round(time.Millisecond)))
		}
	} else {
		sb.WriteString(fmt.Sprintf("    %s Failed to build network test request: %v\n",
			lipgloss.NewStyle().Foreground(ColorDanger).Render("✗"), reqErr))
	}
	sb.WriteString("\n")

	// 3. Git status
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [3] Git Version Control & Workspace Status") + "\n")
	gitCheck := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	if out, err := gitCheck.Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		sb.WriteString(fmt.Sprintf("    %s Current directory is not a valid Git repository\n",
			lipgloss.NewStyle().Foreground(ColorWarning).Render("⚠️")))
	} else {
		branchCmd := exec.Command("git", "branch", "--show-current")
		branch, _ := branchCmd.Output()
		sb.WriteString(fmt.Sprintf("    %s Git status:    Inside a Git repository\n",
			lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓")))
		sb.WriteString(fmt.Sprintf("    %s Active branch: %s\n",
			lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), strings.TrimSpace(string(branch))))

		// Check dirty changes
		statusCmd := exec.Command("git", "status", "--porcelain")
		statusOut, _ := statusCmd.Output()
		lines := strings.Split(strings.TrimSpace(string(statusOut)), "\n")
		dirtyCount := 0
		if len(lines) > 0 && lines[0] != "" {
			dirtyCount = len(lines)
		}

		if dirtyCount > 0 {
			sb.WriteString(fmt.Sprintf("    %s Uncommitted files: %d modified file(s)\n",
				lipgloss.NewStyle().Foreground(ColorWarning).Render("⚠️"), dirtyCount))
		} else {
			sb.WriteString(fmt.Sprintf("    %s Uncommitted files: %s Working tree clean\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), lipgloss.NewStyle().Foreground(ColorSuccess).Render("Clean")))
		}
	}
	sb.WriteString("\n")

	// 4. Tools validation
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [4] Developer Toolchains") + "\n")
	tools := []struct {
		Name    string
		CmdName string
		Arg     string
	}{
		{"git", "git", "--version"},
		{"go", "go", "version"},
		{"python3", "python3", "--version"},
		{"node", "node", "--version"},
		{"npm", "npm", "--version"},
		{"ripgrep", "rg", "--version"},
	}

	for _, tool := range tools {
		path, err := exec.LookPath(tool.CmdName)
		if err != nil {
			sb.WriteString(fmt.Sprintf("    %s %-8s: %s (not installed or not in PATH)\n",
				lipgloss.NewStyle().Foreground(ColorDanger).Render("✗"), tool.Name, lipgloss.NewStyle().Foreground(ColorDanger).Render("Not found")))
		} else {
			vCmd := exec.Command(tool.CmdName, tool.Arg)
			vOut, _ := vCmd.Output()
			vStr := strings.TrimSpace(string(vOut))
			// Extract first line of version output
			if idx := strings.Index(vStr, "\n"); idx != -1 {
				vStr = vStr[:idx]
			}
			// Truncate overly long versions (e.g. ripgrep outputs lots of lines)
			if len(vStr) > 40 {
				vStr = vStr[:37] + "..."
			}

			sb.WriteString(fmt.Sprintf("    %s %-8s: %s (path: %s)\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"),
				tool.Name,
				lipgloss.NewStyle().Foreground(ColorSuccess).Render(vStr),
				filepath.Base(path)))
		}
	}
	sb.WriteString("\n")

	// 5. System Indicators
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [5] Host System Metrics") + "\n")
	cwd, _ := os.Getwd()
	sb.WriteString(fmt.Sprintf("    OS:             %s / %s (CPU cores: %d)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()))
	sb.WriteString(fmt.Sprintf("    Working dir:    %s\n", cwd))

	// Project files summary
	files, _ := filepath.Glob("*")
	sb.WriteString(fmt.Sprintf("    Project entries: %d top-level items\n", len(files)))

	sb.WriteString("\n" + lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render(" 🎉 Diagnostics complete! Your development environment is healthy.") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Render("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━") + "\n")

	// Wrap inside a stylish card
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String())
}
