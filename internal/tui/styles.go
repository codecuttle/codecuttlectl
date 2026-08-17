package tui

import (
	"charm.land/lipgloss/v2"
)

// Color palette: forest greens, sky blues, navies, honey yellows.
// Designed for dark terminal backgrounds. No Background() on text elements
// to avoid partial-highlight artifacts.
var (
	// Primary palette
	colorForestGreen = lipgloss.Color("#52b788")
	colorSkyBlue     = lipgloss.Color("#48cae4")
	colorNavy        = lipgloss.Color("#90b4ce")
	colorHoney       = lipgloss.Color("#f4a261")
	colorRed         = lipgloss.Color("#ef476f")

	// Neutrals — chosen for selection visibility.
	// Most terminals invert fg/bg for selection, so having distinct fg colors
	// from the background ensures highlighted text remains readable.
	colorDim    = lipgloss.Color("#6c757d")
	colorMuted  = lipgloss.Color("#495057")
	colorText   = lipgloss.Color("#e9ecef")
	colorBright = lipgloss.Color("#ffffff")

	// Surfaces (Material Design: elevation via subtle color shifts)
	colorSurface0 = lipgloss.Color("#1a1b1e") // Base (input area fill)
	colorSurface1 = lipgloss.Color("#212529") // Elevated (status/help bars)
	colorSurface2 = lipgloss.Color("#2b2d30") // Higher (active elements)

	// Thinking
	colorThinking = lipgloss.Color("#7b8794")
)

// --- Status Bar: single line, subtle surface ---

var (
	StatusBarStyle = lipgloss.NewStyle().
			Background(colorSurface1)

	StatusLabelStyle = lipgloss.NewStyle().
				Foreground(colorBright).
				Bold(true)

	StatusModelStyle = lipgloss.NewStyle().
				Foreground(colorForestGreen)

	StatusDimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	StatusTokenStyle = lipgloss.NewStyle().
				Foreground(colorHoney)
)

// --- Messages: clean typography, NO backgrounds on text ---

var (
	// User: sky blue prefix, bright text
	UserPrefixStyle = lipgloss.NewStyle().
			Foreground(colorSkyBlue).
			Bold(true)

	UserBodyStyle = lipgloss.NewStyle().
			Foreground(colorBright)

	// Assistant: green prefix, normal text
	AssistantPrefixStyle = lipgloss.NewStyle().
				Foreground(colorForestGreen).
				Bold(true)

	AssistantBodyStyle = lipgloss.NewStyle().
				Foreground(colorText)

	// Reasoning: commands shown with contrasting background, monospace feel
	ReasoningPrefixStyle = lipgloss.NewStyle().
				Foreground(colorThinking).
				Italic(true)

	ReasoningBodyStyle = lipgloss.NewStyle().
				Foreground(colorThinking).
				Background(colorSurface2).
				PaddingLeft(2).
				PaddingRight(1)

	ReasoningCollapsedStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Italic(true)

	// Tool calls
	ToolCallStyle = lipgloss.NewStyle().
			Foreground(colorHoney).
			PaddingLeft(2)

	ToolResultSuccessStyle = lipgloss.NewStyle().
				Foreground(colorForestGreen).
				PaddingLeft(2)

	ToolResultErrorStyle = lipgloss.NewStyle().
				Foreground(colorRed).
				PaddingLeft(2)

	// Streaming cursor
	StreamingCursorStyle = lipgloss.NewStyle().
				Foreground(colorHoney)
)

// --- Input: rounded border, blue/teal accent ---

var (
	InputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorSkyBlue).
			Padding(0, 1)

	InputActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorSkyBlue).
				Padding(0, 1)

	InputSpinnerStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorHoney).
				Padding(0, 1)
)

// --- Todo bar ---

var (
	TodoBarStyle = lipgloss.NewStyle().
			Background(colorSurface1)

	TodoPendingStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	TodoInProgressStyle = lipgloss.NewStyle().
				Foreground(colorHoney).
				Bold(true)

	TodoCompletedStyle = lipgloss.NewStyle().
				Foreground(colorForestGreen)

	TodoCancelledStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Strikethrough(true)

	TodoHighPriorityStyle = lipgloss.NewStyle().
				Foreground(colorRed).
				Bold(true)

	TodoMedPriorityStyle = lipgloss.NewStyle().
				Foreground(colorHoney)

	TodoLowPriorityStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	PanelBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorMuted)
)

// --- Help bar ---

var (
	HelpBarStyle = lipgloss.NewStyle().
			Background(colorSurface1)

	HelpKeyStyle = lipgloss.NewStyle().
			Foreground(colorSkyBlue)

	HelpDescStyle = lipgloss.NewStyle().
			Foreground(colorDim)
)

// --- Spinner ---

var (
	SpinnerStyle = lipgloss.NewStyle().
			Foreground(colorHoney)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)
)
