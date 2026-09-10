package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestThemedScreens(t *testing.T) {
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile); lipgloss.SetHasDarkBackground(dark) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	var confirmations []string
	for _, dark := range []bool{false, true} {
		lipgloss.SetHasDarkBackground(dark)
		m := model{width: 90, height: 28, confirm: true, result: scanResult{Items: []finding{{Path: "/Users/example/Library/Caches/app", Kind: "Cache", Size: 1024}}}}
		view := m.View()
		for _, styled := range []string{
			danger.Bold(true).Render("Move this one item to Trash?"),
			accent.Render(m.result.Items[0].Path),
			danger.Bold(true).Render("[y] Move to Trash"),
			accent.Render("[any other key] Cancel"),
			divider.Render(strings.Repeat("─", m.width-4)),
		} {
			if !strings.Contains(view, styled) {
				t.Fatalf("confirmation missing themed content: %q", styled)
			}
		}
		if strings.Count(view, "\n") >= m.height {
			t.Fatal("short confirmation should fit")
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > m.width {
				t.Fatal("colored confirmation exceeds terminal width")
			}
		}
		confirmations = append(confirmations, view)
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if updated.(model).confirm || updated.(model).busy || cmd != nil {
			t.Fatal("Enter must still cancel")
		}
		for _, tc := range []struct {
			model   model
			markers []string
		}{
			{model{width: 40, height: 16}, []string{accent.Render("wclean"), warning.Render("Please resize to at least 48 × 18.")}},
			{model{confirm: true}, []string{warning.Render("No item selected.")}},
			{model{}, []string{muted.Render("No findings here. Try another tab or clear the filter.")}},
			{model{directory: &finding{Path: "/cache"}}, []string{muted.Render("No listed children. Clear the filter or Backspace to go up.")}},
			{model{showWarnings: true}, []string{muted.Render("No scan warnings.")}},
			{model{showWarnings: true, result: scanResult{Warnings: []string{"First warning", "Second warning"}}}, []string{warning.Render("First warning"), warning.Render("Second warning")}},
			{model{status: "Trash failed", statusError: true}, []string{danger.Render("Trash failed")}},
			{model{status: "Scan complete"}, []string{accent.Render("Scan complete")}},
			{model{scanning: true}, []string{accent.Render("Scanning"), muted.Render("Library and installed apps; q cancels.")}},
			{model{loading: true}, []string{accent.Render("Reading directory"), muted.Render("Sizing children; q cancels.")}},
			{model{busy: true, status: "Moving to Trash"}, []string{accent.Render("Moving to Trash")}},
		} {
			if tc.model.width == 0 {
				tc.model.width = 90
				tc.model.height = 28
			}
			rendered := tc.model.View()
			for _, marker := range tc.markers {
				if !strings.Contains(rendered, marker) {
					t.Fatalf("screen missing themed content: %q", marker)
				}
			}
		}
		// Styling must not bypass the guard on an overflowing full-path confirmation.
		m.height = 18
		m.result.Items[0].Path = "/" + strings.Repeat("very-long-directory/", 100)
		if !strings.Contains(m.View(), warning.Render("Resize the terminal to show the full confirmation.")) {
			t.Fatal("overflow notice is not themed")
		}
		updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		if cmd != nil || updated.(model).busy {
			t.Fatal("overflowing confirmation allowed removal")
		}
	}
	if confirmations[0] == confirmations[1] {
		t.Fatal("light and dark palettes are identical")
	}
	if ansi.Strip(confirmations[0]) != ansi.Strip(confirmations[1]) {
		t.Fatal("theme changed confirmation content")
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	m := model{width: 90, height: 28, confirm: true, result: scanResult{Items: []finding{{Path: "/Users/example/Library/Caches/app", Kind: "Cache", Size: 1024}}}}
	if ansi.Strip(m.View()) != ansi.Strip(confirmations[0]) {
		t.Fatal("monochrome rendering lost confirmation content")
	}
}
