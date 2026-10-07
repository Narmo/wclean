package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m model) popup(content string) string {
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent.GetForeground()).Padding(1, 2).Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

const version = "0.3.0"

func (m model) aboutView() string {
	if m.width < 48 || m.height < 18 {
		return m.message(accent.Render("wclean") + "\n" + warning.Render("Resize to at least 48 × 18.") + "\n" + muted.Render("[esc/enter/?/q] close"))
	}
	width := min(76, m.width-4) - 6
	withLogo := width >= aboutLogo.width+2+38
	textWidth := width
	if withLogo {
		textWidth -= aboutLogo.width + 2
	}
	rule := divider.Render(strings.Repeat("─", textWidth))
	content := []string{
		accent.Render("wclean " + version),
		muted.Render("a little room to breathe"),
		rule,
		"Inspect macOS caches and app data.",
		"Single or batch removal via Trash.",
		warning.Render("Storage removal can break apps."),
		muted.Render("AI requests require your consent."),
		rule,
		muted.Render("Built with Go, Bubble Tea & Lip Gloss."),
		accent.Render("MIT License · © 2026 Nikita Denin"),
		muted.Render("See LICENSE for terms; no warranty."),
		accent.Render("[esc/enter/?/q] close"),
	}
	return m.popup(ansi.Wrap(brandHeader(content, width, withLogo, aboutLogo), width, ""))
}
