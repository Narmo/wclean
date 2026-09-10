package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type batchStage int

const (
	batchPreparing batchStage = iota
	batchPreview
	batchRunning
	batchDone
)

type batchDialog struct {
	id                                     uint64
	stage                                  batchStage
	ctx                                    context.Context
	cancel                                 context.CancelFunc
	items                                  []batchItem
	next, scroll, moved, missing, failures int
	stop, quit                             bool
}
type batchPreparedMsg struct {
	id    uint64
	items []batchItem
}
type batchStepMsg struct {
	id      uint64
	index   int
	missing bool
	err     error
}

func (m model) startBatch() (tea.Model, tea.Cmd) {
	selected := m.selectedItems()
	if len(selected) == 0 {
		m.status = "Nothing checked. Use [space] to select items."
		return m, nil
	}
	m.batchSequence++
	ctx, cancel := context.WithCancel(m.ctx)
	id := m.batchSequence
	m.batch = &batchDialog{id: id, stage: batchPreparing, ctx: ctx, cancel: cancel}
	return m, tea.Batch(func() tea.Msg { return batchPreparedMsg{id, prepareBatch(ctx, m.home, selected)} }, pulse())
}
func (m model) nextBatchCmd() tea.Cmd {
	item, index, id, ctx := m.batch.items[m.batch.next], m.batch.next, m.batch.id, m.batch.ctx
	return func() tea.Msg {
		missing, err := executeBatchItem(ctx, m.home, m.roots, item)
		return batchStepMsg{id, index, missing, err}
	}
}
func (m model) updateBatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.batch == nil {
		return m, nil
	}
	b := m.batch
	switch msg := msg.(type) {
	case batchPreparedMsg:
		if msg.id != b.id {
			return m, nil
		}
		b.items, b.stage = msg.items, batchPreview
	case batchStepMsg:
		if msg.id != b.id || b.stage != batchRunning || msg.index != b.next {
			return m, nil
		}
		item := &b.items[b.next]
		switch {
		case msg.err != nil:
			b.failures++
			item.outcome = "ERROR: " + safe(msg.err.Error())
		case msg.missing:
			b.missing++
			item.outcome = "Missing; remembered paths are retained."
		default:
			b.moved++
			item.outcome = "Moved to Trash."
			// A remembered descendant may not be in the currently open listing.
			if m.checked == nil {
				m.checked = map[string]finding{}
			}
			if _, ok := m.findingAt(item.finding.Path); !ok {
				m.checked[item.finding.Path] = item.finding
			}
			m.status = "Moved to Trash. Restore using Finder → Trash → Put Back."
			m.applyRemoval(item.finding.Path)
			if err := m.rememberCache(item.finding); err != nil {
				b.failures++
				item.outcome += " Could not save future selection: " + safe(err.Error())
				m.result.Warnings = append(m.result.Warnings, item.outcome)
			}
		}
		b.next++
		if b.stop || b.next >= len(b.items) {
			b.stage = batchDone
			b.cancel()
			m.busy = false
			m.status = fmt.Sprintf("Batch complete: %d trashed, %d missing, %d errors.", b.moved, b.missing, b.failures)
			m.statusError = b.failures > 0
			if b.quit {
				return m, tea.Quit
			}
		} else {
			return m, m.nextBatchCmd()
		}
	case tea.KeyMsg:
		key := msg.String()
		if b.stage == batchRunning && (key == "esc" || key == "q" || key == "ctrl+c") {
			b.stop = true
			b.quit = b.quit || key == "ctrl+c"
			return m, nil
		}
		if b.stage != batchRunning && (key == "esc" || key == "q" || key == "ctrl+c" || key == "enter") {
			b.cancel()
			m.batch = nil
			if key == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.width < 48 || m.height < 18 {
			return m, nil
		}
		if key == "y" && b.stage == batchPreview && len(b.items) > 0 {
			b.stage = batchRunning
			m.busy = true
			return m, tea.Batch(m.nextBatchCmd(), pulse())
		}
		_, rows := m.batchDimensions()
		limit := max(0, len(m.batchLines())-rows)
		b.scroll = min(b.scroll, limit)
		switch key {
		case "up", "k":
			b.scroll = max(0, b.scroll-1)
		case "down", "j":
			b.scroll = min(limit, b.scroll+1)
		case "pgup":
			b.scroll = max(0, b.scroll-rows)
		case "pgdown":
			b.scroll = min(limit, b.scroll+rows)
		case "home":
			b.scroll = 0
		case "end":
			b.scroll = limit
		}
	}
	return m, nil
}
func (m model) batchDimensions() (int, int) {
	return max(1, min(100, m.width-4)-6), max(1, min(30, m.height-4)-10)
}
func (m model) batchLines() []string {
	text := "Checking selected paths…"
	if m.batch.stage != batchPreparing {
		text = "Close owning apps first. Parents include their contents; overlapping selections are removed only once. Missing remembered paths stay saved.\n"
		for _, item := range m.batch.items {
			if item.finding.Kind == "Storage" {
				text = storageWarning + "\n" + text
				break
			}
		}
		for _, item := range m.batch.items {
			state := item.outcome
			if state == "" {
				switch {
				case item.err != nil:
					state = "ERROR: " + safe(item.err.Error())
				case item.missing:
					state = "Missing; retained for a future batch."
				case m.batch.stage == batchDone:
					state = "Not attempted."
				default:
					state = "Ready"
				}
			}
			text += fmt.Sprintf("\n%s · %s\n%s\n%s\n", item.finding.Kind, human(item.finding.Size), safe(item.finding.Path), state)
		}
	}
	width, _ := m.batchDimensions()
	return strings.Split(ansi.Wrap(text, width, ""), "\n")
}
func (m model) batchView() string {
	if m.width < 48 || m.height < 18 {
		return m.message(accent.Render("Batch removal") + "\n" + warning.Render("Resize to at least 48 × 18.") + "\n" + muted.Render("[esc] cancel / stop after current item"))
	}
	width, rows := m.batchDimensions()
	var body []string
	add := func(text string) {
		text = ansi.Truncate(text, width, "…")
		body = append(body, text+strings.Repeat(" ", max(0, width-ansi.StringWidth(text))))
	}
	b := m.batch
	var total int64
	for _, item := range b.items {
		if !item.missing && item.err == nil {
			total += item.finding.Size
		}
	}
	title := fmt.Sprintf("Batch · %d targets · %s", len(b.items), human(total))
	if b.stage == batchPreparing {
		title = "Preparing batch" + strings.Repeat(".", m.frame%4)
	}
	if b.stage == batchRunning {
		title = fmt.Sprintf("Removing %d/%d", b.next+1, len(b.items))
		if b.stop {
			title += " · stopping"
		}
	}
	if b.stage == batchDone {
		title = fmt.Sprintf("%d trashed · %d missing · %d errors", b.moved, b.missing, b.failures)
	}
	add(accent.Render(title))
	notice := "Only [y] starts removal; Enter cancels."
	for _, item := range b.items {
		if item.finding.Kind == "Storage" {
			notice = "DANGER: includes unverified Storage"
			break
		}
	}
	add(danger.Render(notice))
	add(divider.Render(strings.Repeat("─", width)))
	lines := m.batchLines()
	start := min(b.scroll, max(0, len(lines)-rows))
	for i := 0; i < rows; i++ {
		text := ""
		if start+i < len(lines) {
			text = lines[start+i]
		}
		add(text)
	}
	add(divider.Render(strings.Repeat("─", width)))
	help := "[esc/enter] close"
	if b.stage == batchPreview {
		help = "[y] Trash batch  [esc/enter] cancel"
	}
	if b.stage == batchRunning {
		help = "[esc] stop after the current item"
	}
	add(muted.Render(help))
	add(muted.Render("[↑↓/PgUp/PgDn] scroll"))
	return m.popup(strings.Join(body, "\n"))
}
