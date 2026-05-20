package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Minimalist Aesthetic Color Palette (Claude-Style Theme)
var (
	ColorPrimary   = lipgloss.Color("#10B981") // Emerald Green
	ColorSecondary = lipgloss.Color("#9CA3AF") // Slate Gray
	ColorSuccess   = lipgloss.Color("#10B981") // Emerald Green
	ColorWarning   = lipgloss.Color("#F59E0B") // Amber Yellow
	ColorDanger    = lipgloss.Color("#EF4444") // Coral Red
	ColorTextMuted = lipgloss.Color("#6B7280") // Cool Gray
)

// Lipgloss Styles
var (
	StylePrompt = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	StyleWelcome = lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Padding(0, 0).
			MarginTop(1).
			MarginBottom(1)

	StyleUserMsg = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")).
			Bold(true).
			MarginLeft(0).
			MarginTop(1)

	StyleAgentHeader = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true).
			MarginTop(1)

	StyleToolHeader = lipgloss.NewStyle().
			Foreground(ColorWarning).
			Bold(true).
			MarginLeft(0).
			MarginTop(1)

	StyleToolSuccess = lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true).
			MarginLeft(0)

	StyleToolError = lipgloss.NewStyle().
			Foreground(ColorDanger).
			Bold(true).
			MarginLeft(0)

	StyleThinking = lipgloss.NewStyle().
			Foreground(ColorWarning).
			Italic(true)

	StyleConfirmCard = lipgloss.NewStyle().
				Padding(0, 0).
				MarginTop(1).
				MarginBottom(1)

	StyleKeyHelp = lipgloss.NewStyle().
			Foreground(ColorTextMuted).
			Italic(true)

	StyleKeyActive = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)
)
