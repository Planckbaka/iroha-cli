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
	sb.WriteString("\n" + titleStyle("🩺 Iroha Environment Doctor — 诊断报告") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Render("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━") + "\n\n")

	// 1. Config Audit
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [1] 核心配置审核 (Core Configuration)") + "\n")
	cfg, err := config.LoadConfig()
	if err != nil {
		sb.WriteString(fmt.Sprintf("    %s 加载配置失败: %v\n", lipgloss.NewStyle().Foreground(ColorDanger).Render("✗"), err))
	} else {
		sb.WriteString(fmt.Sprintf("    提供商 (Provider): %s\n", StylePrompt.Render(cfg.Provider)))
		sb.WriteString(fmt.Sprintf("    选用模型 (Model):    %s\n", StylePrompt.Render(cfg.Model)))
		if cfg.BaseURL != "" {
			sb.WriteString(fmt.Sprintf("    端点地址 (BaseURL):  %s\n", lipgloss.NewStyle().Foreground(ColorPrimary).Render(cfg.BaseURL)))
		} else {
			sb.WriteString("    端点地址 (BaseURL):  官方默认端点\n")
		}

		// API Key mask
		if cfg.APIKey != "" {
			masked := cfg.APIKey
			if len(masked) > 8 {
				masked = masked[:4] + "...." + masked[len(masked)-4:]
			} else {
				masked = "********"
			}
			sb.WriteString(fmt.Sprintf("    凭证状态 (API Key):  %s 已配置 (长度: %d, 预览: %s)\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), len(cfg.APIKey), masked))
		} else {
			sb.WriteString(fmt.Sprintf("    凭证状态 (API Key):  %s 未配置 (请设置环境变量或在 ~/.iroha.json 中配置)\n",
				lipgloss.NewStyle().Foreground(ColorWarning).Render("⚠️")))
		}

		mode := agent.GlobalPermissionManager.GetMode()
		sb.WriteString(fmt.Sprintf("    安全等级 (Permission):%s\n", StylePrompt.Render(string(mode))))
	}
	sb.WriteString("\n")

	// 2. Network Latency
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [2] 网络连通度与 API 延时 (Network Latency)") + "\n")
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
			sb.WriteString(fmt.Sprintf("    %s 目标终点 (%s) 连接失败: %v\n",
				lipgloss.NewStyle().Foreground(ColorDanger).Render("✗"), pingURL, err))
		} else {
			_ = resp.Body.Close()
			sb.WriteString(fmt.Sprintf("    %s 目标连接成功: %s\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), pingURL))
			sb.WriteString(fmt.Sprintf("    %s TCP 握手与响应延迟: %v\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), duration.Round(time.Millisecond)))
		}
	} else {
		sb.WriteString(fmt.Sprintf("    %s 构建网络测试请求失败: %v\n",
			lipgloss.NewStyle().Foreground(ColorDanger).Render("✗"), reqErr))
	}
	sb.WriteString("\n")

	// 3. Git status
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [3] Git 版本控制与工作区状态 (Git Workspace)") + "\n")
	gitCheck := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	if out, err := gitCheck.Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		sb.WriteString(fmt.Sprintf("    %s 当前目录非有效的 Git 托管项目工作区\n",
			lipgloss.NewStyle().Foreground(ColorWarning).Render("⚠️")))
	} else {
		branchCmd := exec.Command("git", "branch", "--show-current")
		branch, _ := branchCmd.Output()
		sb.WriteString(fmt.Sprintf("    %s Git 托管状态:  在 Git 仓库内\n",
			lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓")))
		sb.WriteString(fmt.Sprintf("    %s 当前活动分支:  %s\n",
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
			sb.WriteString(fmt.Sprintf("    %s 未提交的文件:  %d 个修改文件\n",
				lipgloss.NewStyle().Foreground(ColorWarning).Render("⚠️"), dirtyCount))
		} else {
			sb.WriteString(fmt.Sprintf("    %s 未提交的文件:  %s 工作区十分干净\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"), lipgloss.NewStyle().Foreground(ColorSuccess).Render("Clean")))
		}
	}
	sb.WriteString("\n")

	// 4. Tools validation
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [4] 开发者工具链探查 (Developer Toolchains)") + "\n")
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
			sb.WriteString(fmt.Sprintf("    %s %-8s: %s (未安装或不在 PATH 中)\n",
				lipgloss.NewStyle().Foreground(ColorDanger).Render("✗"), tool.Name, lipgloss.NewStyle().Foreground(ColorDanger).Render("未就绪")))
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

			sb.WriteString(fmt.Sprintf("    %s %-8s: %s (路径: %s)\n",
				lipgloss.NewStyle().Foreground(ColorSuccess).Render("✓"),
				tool.Name,
				lipgloss.NewStyle().Foreground(ColorSuccess).Render(vStr),
				filepath.Base(path)))
		}
	}
	sb.WriteString("\n")

	// 5. System Indicators
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" [5] 宿主系统健康度指标 (System Metrics)") + "\n")
	cwd, _ := os.Getwd()
	sb.WriteString(fmt.Sprintf("    操作系统:     %s / %s (CPU 核心数: %d)\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU()))
	sb.WriteString(fmt.Sprintf("    当前工作目录:  %s\n", cwd))

	// Project files summary
	files, _ := filepath.Glob("*")
	sb.WriteString(fmt.Sprintf("    项目目录条数:  %d 个顶层条目\n", len(files)))

	sb.WriteString("\n" + lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render(" 🎉 诊断完成！您的开发环境状态良好。") + "\n")
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
