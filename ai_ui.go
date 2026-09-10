package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type aiStage int

const (
	aiPreparing aiStage = iota
	aiPreview
	aiWaiting
	aiAnswer
)

type aiDialog struct {
	id               uint64
	stage            aiStage
	clients          []aiClient
	selected, scroll int
	prompt, answer   string
	failed           bool
	ctx              context.Context
	cancel           context.CancelFunc
}
type aiPreparedMsg struct {
	id      uint64
	clients []aiClient
	prompt  string
	err     error
}
type aiReplyMsg struct {
	id   uint64
	text string
	err  error
}

func (m model) startAI(f finding) (tea.Model, tea.Cmd) {
	m.aiSequence++
	ctx, cancel := context.WithCancel(m.ctx)
	id := m.aiSequence
	m.ai = &aiDialog{id: id, ctx: ctx, cancel: cancel, stage: aiPreparing}
	return m, tea.Batch(func() tea.Msg {
		prompt, err := prepareAIPrompt(ctx, m.home, f)
		return aiPreparedMsg{id, installedAIClients(), prompt, err}
	}, pulse())
}

func (m model) updateAI(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.ai == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case aiPreparedMsg:
		if msg.id != m.ai.id {
			return m, nil
		}
		m.ai.clients, m.ai.prompt = msg.clients, msg.prompt
		if msg.err != nil || len(msg.clients) == 0 {
			m.ai.stage, m.ai.failed = aiAnswer, true
			m.ai.answer = "No supported AI client found on PATH. Install and sign in to Claude Code, Codex, or pi outside wclean, then try again."
			if msg.err != nil {
				m.ai.answer = cleanAIText(msg.err.Error())
			}
			m.ai.cancel()
		} else {
			m.ai.stage = aiPreview
			for i, client := range m.ai.clients {
				if client.name == m.aiLast {
					m.ai.selected = i
				}
			}
		}
	case aiReplyMsg:
		if msg.id != m.ai.id {
			return m, nil
		}
		m.ai.stage, m.ai.scroll, m.ai.failed = aiAnswer, 0, msg.err != nil
		m.ai.answer = msg.text
		if msg.err != nil {
			m.ai.answer = cleanAIText(msg.err.Error())
		}
		m.ai.cancel()
	case tea.KeyMsg:
		key := msg.String()
		if key == "esc" || key == "q" || key == "ctrl+c" {
			m.ai.cancel()
			m.ai = nil
			if key == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.width < 48 || m.height < 18 {
			return m, nil
		}
		switch key {
		case "left", "right":
			if m.ai.stage == aiPreview && len(m.ai.clients) > 0 {
				delta := 1
				if key == "left" {
					delta = len(m.ai.clients) - 1
				}
				m.ai.selected = (m.ai.selected + delta) % len(m.ai.clients)
			}
		case "enter":
			if m.ai.stage == aiPreview && len(m.ai.clients) > 0 {
				client := m.ai.clients[m.ai.selected]
				m.aiLast = client.name
				m.ai.stage, m.ai.scroll = aiWaiting, 0
				ctx, id, prompt := m.ai.ctx, m.ai.id, m.ai.prompt
				return m, tea.Batch(func() tea.Msg { text, err := askAI(ctx, client, prompt); return aiReplyMsg{id, text, err} }, pulse())
			}
		case "up", "k", "pgup", "down", "j", "pgdown", "home", "end":
			_, rows := m.aiDimensions()
			limit := max(0, len(m.aiLines())-rows)
			m.ai.scroll = min(m.ai.scroll, limit)
			step := 1
			if key == "pgup" || key == "pgdown" {
				step = rows
			}
			switch key {
			case "up", "k", "pgup":
				m.ai.scroll = max(0, m.ai.scroll-step)
			case "down", "j", "pgdown":
				m.ai.scroll = min(limit, m.ai.scroll+step)
			case "home":
				m.ai.scroll = 0
			case "end":
				m.ai.scroll = limit
			}
		}
	}
	return m, nil
}

func (m model) aiDimensions() (width, rows int) {
	return max(1, min(100, m.width-4)-6), max(1, min(30, m.height-4)-11)
}
func (m model) aiLines() []string {
	text := "Preparing a metadata-only preview…"
	switch m.ai.stage {
	case aiPreview:
		text = m.ai.prompt
	case aiWaiting:
		text = "Asking: What is stored here?\n\nWaiting for the selected CLI. Press Esc to cancel. Requests time out after two minutes."
	case aiAnswer:
		text = m.ai.answer
	}
	width, _ := m.aiDimensions()
	return strings.Split(ansi.Wrap(cleanAIText(text), width, ""), "\n")
}

func (m model) aiView() string {
	if m.width < 48 || m.height < 18 {
		return m.message(accent.Render("AI explanation") + "\n" + warning.Render("Resize to at least 48 × 18.") + "\n" + muted.Render("[esc] close  [ctrl+c] quit"))
	}
	width, rows := m.aiDimensions()
	var body []string
	add := func(text string) {
		text = ansi.Truncate(text, width, "…")
		body = append(body, text+strings.Repeat(" ", max(0, width-ansi.StringWidth(text))))
	}
	add(accent.Render("AI · What is stored here?"))
	clients := "Client: "
	for i, client := range m.ai.clients {
		label := client.name
		if i == m.ai.selected {
			label = selected.Render("[" + label + "]")
		}
		clients += label + "  "
	}
	add(clients)
	notice := "Sends metadata; CLI quota may apply."
	if m.ai.stage == aiPreparing {
		notice = "Preparing" + strings.Repeat(".", m.frame%4)
	}
	if m.ai.stage == aiWaiting {
		notice = "Waiting" + strings.Repeat(".", m.frame%4)
	}
	if m.ai.stage == aiAnswer {
		notice = "AI advice can be wrong. Verify first."
	}
	add(warning.Render(notice))
	add(divider.Render(strings.Repeat("─", width)))
	lines := m.aiLines()
	start := min(m.ai.scroll, max(0, len(lines)-rows))
	for i := 0; i < rows; i++ {
		text := ""
		if start+i < len(lines) {
			text = lines[start+i]
		}
		if m.ai.failed {
			text = danger.Render(text)
		}
		add(text)
	}
	add(divider.Render(strings.Repeat("─", width)))
	help := "[esc/q] close"
	if m.ai.stage == aiPreview {
		help = "[←→] client [enter] send [esc] close"
	}
	if m.ai.stage == aiWaiting || m.ai.stage == aiPreparing {
		help = "[esc/q] cancel"
	}
	add(muted.Render(help))
	add(muted.Render(fmt.Sprintf("[↑↓/PgUp/PgDn] scroll  %d–%d/%d", start+1, min(start+rows, len(lines)), len(lines))))
	return m.popup(strings.Join(body, "\n"))
}
