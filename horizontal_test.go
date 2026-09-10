package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestHorizontalScroll(t *testing.T) {
	for _, warnings := range []bool{false, true} {
		text := strings.Repeat("界é", 60) + "END"
		m := model{width: 48, height: 18, showWarnings: warnings, result: scanResult{
			Items: []finding{{Path: "/Library/Caches/" + text, Kind: "Cache"}}, Warnings: []string{text},
		}}
		for i := 0; i < 100; i++ {
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
			m = updated.(model)
		}
		if m.tab != 0 || m.showWarnings != warnings || m.xOffset == 0 {
			t.Fatal("horizontal navigation changed category or failed to scroll")
		}
		view := ansi.Strip(m.View())
		row := strings.Split(view, "\n")[5]
		prefix := "  > ‹"
		if !warnings {
			prefix = "  > [ ] ‹"
		}
		if !strings.HasPrefix(row, prefix) || !strings.HasSuffix(row, "END") || !utf8.ValidString(row) || ansi.StringWidth(row) > m.width {
			t.Fatalf("invalid scrolled row: %q", row)
		}
		before := m.xOffset
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		m = updated.(model)
		if m.xOffset != max(0, before-8) {
			t.Fatal("left did not scroll back")
		}
		updated, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 28})
		m = updated.(model)
		if m.xOffset != 0 {
			t.Fatal("resize did not reset scroll")
		}
		m.xOffset = 8
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(model)
		if m.tab != 1 || m.showWarnings || m.xOffset != 0 {
			t.Fatal("Tab did not change category and reset scroll")
		}
	}
	m := model{width: 48, height: 18}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if updated.(model).xOffset != 0 {
		t.Fatal("empty list scrolled")
	}
}
