package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestFilterBlockCursor(t *testing.T) {
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile); lipgloss.SetHasDarkBackground(dark) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	for _, dark := range []bool{false, true} {
		lipgloss.SetHasDarkBackground(dark)
		for _, query := range []string{"", "cache", strings.Repeat("界é", 40) + "_END"} {
			m := model{width: 48, height: 18, searching: true, query: query}
			line := m.filterLine(m.width - 4)
			plain := ansi.Strip(line)
			if !strings.Contains(line, filterCursor.Render("█")) || strings.Contains(plain, "▏") {
				t.Fatal("filter does not use the block cursor")
			}
			if ansi.StringWidth(line) > m.width-4 || !strings.HasSuffix(plain, "  ·  0 checked") {
				t.Fatal("filter cursor or counter exceeds the row")
			}
			if len(query) > 30 && !strings.Contains(plain, "_END█") {
				t.Fatal("long filter hides the insertion point")
			}
			if m.query != query {
				t.Fatal("scrolling the displayed query changed its value")
			}
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = updated.(model)
			if strings.Contains(ansi.Strip(m.filterLine(m.width-4)), "█") || m.query != query {
				t.Fatal("inactive filter still has a cursor or lost its query")
			}
		}
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	m := model{searching: true}
	if !strings.Contains(m.filterLine(44), "█") {
		t.Fatal("monochrome terminal lost the cursor shape")
	}
}
