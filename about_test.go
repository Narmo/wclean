package main

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestAboutPopup(t *testing.T) {
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile); lipgloss.SetHasDarkBackground(dark) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	base := model{width: 90, height: 28, cursor: 1, tab: 1, query: "keep", result: scanResult{Items: []finding{{Path: "keep1", Kind: "Cache"}, {Path: "keep2", Kind: "Cache"}}}}
	question := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
	updated, cmd := base.Update(question)
	m := updated.(model)
	if cmd != nil || !m.about {
		t.Fatal("? did not open About locally")
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'d'}}, {Type: tea.KeyRunes, Runes: []rune{'a'}}, {Type: tea.KeyDown}} {
		updated, cmd = m.Update(key)
		if cmd != nil || updated.(model).confirm || updated.(model).ai != nil || updated.(model).cursor != base.cursor {
			t.Fatal("About keys leaked into findings")
		}
	}
	for _, dark := range []bool{false, true} {
		lipgloss.SetHasDarkBackground(dark)
		for _, size := range [][2]int{{48, 18}, {90, 28}, {140, 50}} {
			m.width, m.height = size[0], size[1]
			rendered := m.View()
			if !strings.Contains(rendered, accent.Render("wclean "+version)) || !strings.Contains(rendered, warning.Render("Storage removal can break apps.")) {
				t.Fatal("About is not themed")
			}
			view := ansi.Strip(rendered)
			if strings.Contains(view, "About wclean") {
				t.Fatal("popup title must be the app name only")
			}
			if m.width >= 80 && !strings.Contains(view, "▀") {
				t.Fatal("About is missing the pixel logo")
			}
			if !strings.Contains(view, "MIT License · © 2026 Nikita Denin") {
				t.Fatal("About is missing license information")
			}
			if lipgloss.Width(view) > m.width || lipgloss.Height(view) > m.height {
				t.Fatal("About exceeds the terminal")
			}
			top, bottom, left := -1, -1, -1
			for i, line := range strings.Split(view, "\n") {
				if p := strings.Index(line, "╭"); p >= 0 {
					top, left = i, p
				}
				if strings.Contains(line, "╰") {
					bottom = i
				}
			}
			if top < 0 || bottom < 0 {
				t.Fatal("About border missing")
			}
			if delta := top - (m.height - bottom - 1); delta < -1 || delta > 1 {
				t.Fatal("About is not vertically centered")
			}
			if delta := left - (m.width - left - min(76, m.width-4)); delta < -1 || delta > 1 {
				t.Fatal("About is not horizontally centered")
			}
		}
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyEnter}, question, {Type: tea.KeyRunes, Runes: []rune{'q'}}} {
		updated, cmd = m.Update(key)
		closed := updated.(model)
		if cmd != nil || closed.about || closed.cursor != base.cursor || closed.query != base.query || closed.tab != base.tab {
			t.Fatal("closing About changed the underlying view")
		}
	}
	if !strings.Contains(base.View(), "[?] About") {
		t.Fatal("About key is missing from the footer")
	}
	base.searching = true
	updated, _ = base.Update(question)
	if updated.(model).about || updated.(model).query != "keep?" {
		t.Fatal("About hijacked filter input")
	}
	base.searching = false
	base.confirm = true
	updated, _ = base.Update(question)
	if updated.(model).about || updated.(model).confirm {
		t.Fatal("? must cancel a pending removal, not cover it")
	}
}

func TestAboutOverAIAndDuringScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := model{ai: &aiDialog{ctx: ctx, cancel: cancel, stage: aiWaiting}}
	question := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
	updated, _ := m.Update(question)
	m = updated.(model)
	if !m.about {
		t.Fatal("About unavailable over AI")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.about || m.ai == nil || ctx.Err() != nil {
		t.Fatal("closing About interrupted AI")
	}
	updated, _ = m.Update(question)
	m = updated.(model)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || ctx.Err() == nil {
		t.Fatal("quitting About did not cancel AI")
	}
	m = model{scanning: true}
	updated, _ = m.Update(question)
	m = updated.(model)
	if !m.about {
		t.Fatal("About unavailable during scan")
	}
	updated, _ = m.Update(scanResult{})
	if !updated.(model).about {
		t.Fatal("scan completion dismissed About")
	}
}
