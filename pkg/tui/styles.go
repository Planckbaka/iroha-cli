package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Cyber-Holographic Color Palette (Iroha Code Theme)
var (
	ColorPrimary   = lipgloss.Color("#22D3EE") // Electric Cyan/Turquoise
	ColorSecondary = lipgloss.Color("#EC4899") // Neon Hot Pink
	ColorSuccess   = lipgloss.Color("#10B981") // Cyber Emerald
	ColorWarning   = lipgloss.Color("#F59E0B") // Amber
	ColorDanger    = lipgloss.Color("#E11D48") // Rose/Magenta
	ColorTextMuted = lipgloss.Color("#64748B") // Slate
)

// Lipgloss Styles
var (
	StylePrompt = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	StyleWelcome = lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Padding(1, 2).
			MarginTop(1).
			MarginBottom(1)

	StyleUserMsg = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4F4F5")).
			Bold(true).
			MarginLeft(1).
			MarginTop(1)

	StyleAgentMsg = lipgloss.NewStyle().
			MarginLeft(1).
			MarginTop(1)

	StyleAgentHeader = lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Bold(true).
				MarginTop(1).
				MarginLeft(1)

	StyleToolHeader = lipgloss.NewStyle().
			Foreground(ColorWarning).
			Bold(true).
			MarginLeft(1).
			MarginTop(1)

	StyleToolSuccess = lipgloss.NewStyle().
				Foreground(ColorSuccess).
				Bold(true).
				MarginLeft(1)

	StyleToolError = lipgloss.NewStyle().
			Foreground(ColorDanger).
			Bold(true).
			MarginLeft(1)

	StyleThinking = lipgloss.NewStyle().
			Foreground(ColorSecondary). // Secondary gray spinner is subtle
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

	StyleStatusBar = lipgloss.NewStyle().
			Background(lipgloss.Color("#1E1B4B")).
			Foreground(lipgloss.Color("#22D3EE")).
			Bold(true)

	StyleDiffAdd = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	StyleDiffDel = lipgloss.NewStyle().
			Foreground(ColorDanger)
)


