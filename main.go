package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	accent       = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#006FBA", Dark: "#22D3EE"}).Bold(true)
	muted        = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#5B527A", Dark: "#B7A8D5"})
	divider      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A78BFA", Dark: "#6D5BD0"})
	filterCursor = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#202020", Dark: "#DADADA"})
	warning      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A84B00", Dark: "#FFBE0B"})
	danger       = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#CE164A", Dark: "#FF5C8A"})
	selected     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#071A2B"}).Background(lipgloss.AdaptiveColor{Light: "#006FBA", Dark: "#22D3EE"}).Bold(true)
)

type tickMsg struct{}
type trashMsg struct {
	path string
	err  error
}
type revealMsg struct{ err error }
type directoryMsg struct {
	dir    finding
	result scanResult
	focus  string
	err    error
}
type model struct {
	ctx                                              context.Context
	home                                             string
	roots                                            []string
	result                                           scanResult
	directory                                        *finding
	children                                         []finding
	loading, statusError                             bool
	width, height, cursor, tab, frame, xOffset       int
	scanning, busy, confirm, showWarnings, searching bool
	query, status                                    string
	ai                                               *aiDialog
	aiSequence                                       uint64
	aiLast                                           string
	about                                            bool
	checked                                          map[string]finding
	remembered                                       map[string]bool
	memoryError                                      error
	memoryDirty                                      bool
	batch                                            *batchDialog
	batchSequence                                    uint64
}

func (m model) scanCmd() tea.Msg {
	result := scan(m.ctx, m.home, m.roots)
	m.addMissingMemory(&result)
	if m.memoryError != nil {
		result.Warnings = append(result.Warnings, "Cache preferences: "+m.memoryError.Error())
	}
	return result
}
func (m model) directoryCmd(dir finding, focus string) tea.Cmd {
	return func() tea.Msg {
		dir, result, err := readDirectory(m.ctx, m.home, dir)
		return directoryMsg{dir: dir, result: result, focus: focus, err: err}
	}
}
func pulse() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}
func (m model) Init() tea.Cmd { return tea.Batch(m.scanCmd, pulse()) }
func (m model) pageRows() int { return max(1, m.height-14) }
func (m model) visible() []finding {
	var items []finding
	source := m.result.Items
	if m.directory != nil {
		source = m.children
	}
	for _, f := range source {
		if m.directory == nil && (m.tab == 1 && f.Kind != "Cache" || m.tab == 2 && f.Kind != "Leftover" || m.tab == 3 && f.Kind != "Storage") {
			continue
		}
		if strings.Contains(strings.ToLower(f.Path+" "+f.Label), strings.ToLower(m.query)) {
			items = append(items, f)
		}
	}
	return items
}
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case aiPreparedMsg, aiReplyMsg:
		return m.updateAI(msg)
	case batchPreparedMsg, batchStepMsg:
		return m.updateBatch(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.xOffset = 0
	case tickMsg:
		m.frame++
		if m.scanning || m.loading || m.busy || m.ai != nil && (m.ai.stage == aiPreparing || m.ai.stage == aiWaiting) || m.batch != nil && m.batch.stage == batchPreparing {
			return m, pulse()
		}
	case scanResult:
		m.checked = map[string]finding{}
		m.statusError = false
		m.result = msg
		m.xOffset = 0
		m.scanning = false
		m.cursor = 0
		if !strings.HasPrefix(m.status, "Moved to Trash.") {
			m.status = "Scan complete. Nothing checked for batch removal."
			if len(m.remembered) > 0 {
				m.status = "Scan complete. Remembered caches are checked; [b] previews removal."
			}
		}
		if m.directory != nil {
			for _, f := range m.result.Items {
				if f.Path == m.directory.origin().Path {
					m.loading = true
					return m, tea.Batch(m.directoryCmd(*m.directory, ""), pulse())
				}
			}
			m.directory, m.children = nil, nil
			m.query = ""
			m.status += " Directory no longer detected; returned to findings."
		}
	case directoryMsg:
		m.statusError = msg.err != nil
		m.loading = false
		m.xOffset = 0
		if msg.err != nil {
			m.status = msg.err.Error()
			m.result.Warnings = append(m.result.Warnings, m.status)
			if m.directory != nil && m.directory.Path == msg.dir.Path {
				m.directory, m.children = nil, nil
				m.cursor = 0
			}
		} else {
			m.directory, m.children = &msg.dir, msg.result.Items
			m.result.Warnings = append(m.result.Warnings, msg.result.Warnings...)
			m.query, m.cursor = "", 0
			for i, f := range m.visible() {
				if f.Path == msg.focus {
					m.cursor = i
					break
				}
			}
			if !strings.HasPrefix(m.status, "Moved to Trash.") {
				m.status = "Directory loaded. d removes only the focused entry."
				if msg.dir.Kind == "Storage" {
					m.status = "Storage may contain valuable data. [d] opens a danger confirmation."
				}
			}
		}
	case trashMsg:
		m.statusError = msg.err != nil
		m.busy = false
		if msg.err != nil {
			m.status = msg.err.Error()
		} else {
			m.status = "Moved to Trash. Restore using Finder → Trash → Put Back."
			removed, found := m.findingAt(msg.path)
			m.applyRemoval(msg.path)
			if found {
				if err := m.rememberCache(removed); err != nil {
					m.statusError = true
					m.status += " Could not remember this cache: " + err.Error()
					m.result.Warnings = append(m.result.Warnings, m.status)
				}
			}
		}
	case revealMsg:
		if msg.err != nil {
			m.statusError = true
			m.status = msg.err.Error()
		}
	case tea.KeyMsg:
		key := msg.String()
		if m.about {
			switch key {
			case "esc", "enter", "?", "q":
				m.about = false
			case "ctrl+c":
				if m.ai != nil {
					m.ai.cancel()
				}
				if m.batch != nil {
					m.batch.cancel()
				}
				return m, tea.Quit
			}
			return m, nil
		}
		if key == "?" && !m.busy && !m.confirm && !m.searching {
			m.about = true
			return m, nil
		}
		if m.ai != nil {
			return m.updateAI(msg)
		}
		if m.batch != nil {
			return m.updateBatch(msg)
		}
		if m.busy {
			return m, nil
		}
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.scanning || m.loading {
			if key == "q" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.searching {
			switch key {
			case "enter":
				m.searching = false
			case "esc":
				m.searching = false
				m.query = ""
			case "backspace":
				r := []rune(m.query)
				if len(r) > 0 {
					m.query = string(r[:len(r)-1])
				}
			default:
				if msg.Type == tea.KeyRunes {
					m.query += string(msg.Runes)
				}
			}
			m.cursor = 0
			return m, nil
		}
		if m.confirm {
			m.confirm = false
			if key == "y" && m.width >= 48 && m.height >= 18 && strings.Count(m.confirmation(), "\n") < m.height {
				items := m.visible()
				if len(items) > 0 {
					f := items[m.cursor]
					m.busy = true
					m.statusError = false
					m.status = "Moving to Trash… respond to any macOS Automation prompt."
					return m, tea.Batch(func() tea.Msg { return trashMsg{f.Path, trash(m.ctx, m.home, m.roots, f)} }, pulse())
				}
			}
			return m, nil
		}
		if key != "left" && key != "right" {
			m.xOffset = 0
		}
		switch key {
		case "left":
			m.xOffset = max(0, m.xOffset-8)
		case "right":
			longest := 0
			if m.showWarnings && len(m.result.Warnings) > 0 {
				longest = ansi.StringWidth(safe(m.result.Warnings[m.cursor]))
			} else if items := m.visible(); len(items) > 0 {
				f := items[m.cursor]
				longest = max(ansi.StringWidth(findingRow(f)), ansi.StringWidth(safe(f.Path)), ansi.StringWidth(safe(f.Reason)))
			}
			overhead := 7
			if !m.showWarnings {
				overhead += 4
			}
			m.xOffset = min(m.xOffset+8, max(0, longest-max(1, m.width-overhead)))
		case "q":
			return m, tea.Quit
		case "enter":
			m.statusError = false
			items := m.visible()
			if !m.showWarnings && len(items) > 0 {
				f := items[m.cursor]
				if f.info == nil || !f.info.IsDir() {
					m.status = "Not a directory; o reveals the file in Finder."
				} else {
					m.loading, m.status = true, ""
					return m, tea.Batch(m.directoryCmd(f, ""), pulse())
				}
			}
		case "backspace":
			if m.showWarnings {
				m.showWarnings, m.cursor = false, 0
			} else if m.directory != nil {
				dir := *m.directory
				if dir.parent != nil {
					m.loading, m.status = true, ""
					return m, tea.Batch(m.directoryCmd(*dir.parent, dir.Path), pulse())
				}
				m.directory, m.children = nil, nil
				m.query, m.cursor, m.status = "", 0, "Returned to findings."
				m.statusError = false
				for i, f := range m.visible() {
					if f.Path == dir.Path {
						m.cursor = i
						break
					}
				}
			}
		case "w":
			m.showWarnings = !m.showWarnings
			m.cursor = 0
		case "esc":
			m.showWarnings = false
			m.query = ""
			m.cursor = 0
		case "j", "down", "pgdown":
			step := 1
			if key == "pgdown" {
				step = m.pageRows()
			}
			n := len(m.visible())
			if m.showWarnings {
				n = len(m.result.Warnings)
			}
			m.cursor = min(m.cursor+step, max(0, n-1))
		case "k", "up", "pgup":
			step := 1
			if key == "pgup" {
				step = m.pageRows()
			}
			m.cursor = max(0, m.cursor-step)
		case "tab", "l":
			if m.directory != nil {
				m.query = ""
			}
			m.directory, m.children = nil, nil
			m.tab = (m.tab + 1) % 4
			m.cursor = 0
			m.showWarnings = false
		case "shift+tab", "h":
			if m.directory != nil {
				m.query = ""
			}
			m.directory, m.children = nil, nil
			m.tab = (m.tab + 3) % 4
			m.cursor = 0
			m.showWarnings = false
		case "/":
			m.searching = true
			m.showWarnings = false
			m.cursor = 0
		case "r":
			if !m.scanning {
				m.scanning = true
				m.result = scanResult{}
				m.cursor = 0
				m.status = ""
				m.statusError = false
				return m, tea.Batch(m.scanCmd, pulse())
			}
		case " ":
			items := m.visible()
			if !m.showWarnings && len(items) > 0 {
				m.statusError = false
				m.toggleChecked(items[m.cursor])
			}
		case "b":
			if !m.showWarnings {
				return m.startBatch()
			}
		case "d":
			items := m.visible()
			if !m.scanning && !m.showWarnings && len(items) > 0 {
				if items[m.cursor].Missing {
					m.statusError = false
					m.status = "Remembered path is unavailable. [space] forgets it; [b] checks it again."
				} else if items[m.cursor].Incomplete {
					m.statusError = true
					m.status = "Removal blocked: incomplete scan. Press w to inspect warnings."
				} else {
					m.confirm = true
				}
			}
		case "a":
			items := m.visible()
			if !m.showWarnings && len(items) > 0 {
				return m.startAI(items[m.cursor])
			}
		case "o":
			items := m.visible()
			if !m.showWarnings && len(items) > 0 {
				path := items[m.cursor].Path
				return m, func() tea.Msg { return revealMsg{exec.Command("/usr/bin/open", "-R", path).Run()} }
			}
		}
	}
	return m, nil
}

// Update the existing scan snapshot without rewalking file trees.
func (m *model) applyRemoval(path string) {
	removed, found := m.findingAt(path)
	if !found {
		return
	} // Ignore duplicate completion messages.
	ancestors := map[string]os.FileInfo{}
	refresh := func(parent finding) {
		if _, ok := ancestors[parent.Path]; ok {
			return
		}
		info, err := validateLocation(m.home, parent)
		ancestors[parent.Path] = info
		if err != nil {
			m.result.Warnings = append(m.result.Warnings, fmt.Sprintf("Ancestor changed after removal: %s: %v", parent.Path, err))
			m.status += " Ancestor metadata could not be refreshed; press r to rescan."
		}
	}
	for parent := removed.parent; parent != nil; parent = parent.parent {
		refresh(*parent)
	}
	// Storage rows include nested cache findings, even when opened directly from
	// the Caches tab. Keep both representations accurate without another scan.
	for _, f := range m.result.Items {
		if strings.HasPrefix(path, f.Path+string(filepath.Separator)) {
			refresh(f)
		}
	}
	// Directory snapshots can be shared by siblings, or copied into the root
	// listing. Adjust each snapshot once without trusting a replaced ancestor.
	seen := map[*finding]bool{}
	var adjust func(*finding)
	adjust = func(f *finding) {
		if f == nil || seen[f] {
			return
		}
		seen[f] = true
		if info, ok := ancestors[f.Path]; ok {
			f.Size = max(0, f.Size-removed.Size)
			if info != nil && f.info != nil && os.SameFile(f.info, info) {
				f.info = info
			} else {
				f.Incomplete = true
			}
		}
		adjust(f.parent)
	}
	for i := range m.result.Items {
		adjust(&m.result.Items[i])
	}
	for i := range m.children {
		adjust(&m.children[i])
	}
	adjust(m.directory)
	for path, f := range m.checked {
		adjust(&f)
		m.checked[path] = f
	}
	removedTree := func(f finding) bool {
		return f.Path == path || strings.HasPrefix(f.Path, path+string(filepath.Separator))
	}
	m.result.Items = slices.DeleteFunc(m.result.Items, removedTree)
	m.children = slices.DeleteFunc(m.children, removedTree)
	for selected := range m.checked {
		if withinPath(path, selected) {
			delete(m.checked, selected)
		}
	}
	m.result.sort()
	m.cursor = max(0, min(m.cursor, len(m.visible())-1))
	m.xOffset = 0
}

func findingRow(f finding) string {
	size := human(f.Size)
	if f.Missing {
		size = "unavailable"
	} else if f.Incomplete {
		size = ">= " + size
	}
	name := safe(filepath.Base(f.Path))
	if f.Label != "" {
		name = safe(f.Label)
	}
	if f.info != nil && f.info.IsDir() {
		name += "/"
	}
	if f.Kind == "Storage" {
		name += " " + danger.Bold(true).Render("[danger]")
	}
	return fmt.Sprintf("%11s  %-9s %s", size, f.Kind, name)
}

// Scroll in terminal cells, preserving Unicode graphemes and the cursor column.
func (m model) scrollText(text string, width int) string {
	if m.xOffset == 0 || ansi.StringWidth(text) <= width {
		return ansi.Truncate(text, width, "…")
	}
	width = max(1, width-1)
	end := max(0, ansi.StringWidth(text)-width)
	offset := min(m.xOffset, end)
	tail := ansi.TruncateLeft(text, offset, "")
	// At the end, skip a wide grapheme straddling the left edge so the suffix fits.
	for offset >= end && ansi.StringWidth(tail) > width {
		offset++
		tail = ansi.TruncateLeft(text, offset, "")
	}
	return "‹" + ansi.Truncate(tail, width, "…")
}

func human(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	for _, unit := range units {
		v /= 1024
		if v < 1024 || unit == "PiB" {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
	}
	return ""
}

// Filesystem names and errors are untrusted terminal text.
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return '�'
		}
		return r
	}, s)
}
func (m model) filterLine(width int) string {
	prefix := muted.Render("Filter: ")
	suffix := ""
	// A running scan has no sizes or availability yet, so any total would be wrong.
	if !m.scanning {
		selected := m.selectedItems()
		label := fmt.Sprintf("  ·  %d checked", len(selected))
		if len(selected) > 0 {
			size, unavailable := m.selectedTotals(selected)
			label += " (" + human(size)
			if unavailable > 0 {
				label += fmt.Sprintf(", %d unavailable", unavailable)
			}
			label += ")"
		}
		suffix = muted.Render(label)
	}
	cursor := ""
	if m.searching {
		cursor = filterCursor.Render("█")
	}
	available := max(0, width-ansi.StringWidth(prefix+cursor+suffix))
	text := safe(m.query)
	if m.searching && ansi.StringWidth(text) > available {
		if available == 0 {
			text = ""
		} else {
			offset := ansi.StringWidth(text) - available + 1
			tail := ansi.TruncateLeft(text, offset, "")
			for ansi.StringWidth(tail) > available-1 {
				offset++
				tail = ansi.TruncateLeft(text, offset, "")
			}
			text = "‹" + tail
		}
	} else {
		text = ansi.Truncate(text, available, "…")
	}
	return prefix + text + cursor + suffix
}

func (m model) View() string {
	if m.about {
		return m.aboutView()
	}
	if m.ai != nil {
		return m.aiView()
	}
	if m.batch != nil {
		return m.batchView()
	}
	if m.width < 48 || m.height < 18 {
		return m.message(accent.Render("wclean") + "\n" + warning.Render("Please resize to at least 48 × 18.") + "\n" + muted.Render("q / Ctrl+C quit (after pending Trash completes)."))
	}
	if m.confirm {
		text := m.confirmation()
		if strings.Count(text, "\n") >= m.height {
			return m.message(accent.Render("wclean") + "\n" + warning.Render("Resize the terminal to show the full confirmation.") + "\n" + muted.Render("Any key cancels; removal is blocked until it fits."))
		}
		return text
	}
	width := m.width - 4
	line := func(s string) string { return ansi.Truncate(s, width, "…") }
	var b strings.Builder
	add := func(s string) { b.WriteString("  " + line(s) + "\n") }
	rule := func(label string) {
		prefix := ""
		if label != "" {
			prefix = divider.Render("─ ") + muted.Render(ansi.Truncate(label, width-4, "…")) + " "
		}
		add(prefix + divider.Render(strings.Repeat("─", width-ansi.StringWidth(prefix))))
	}
	header := []string{accent.Render("wclean") + muted.Render("  /  a little room to breathe")}
	var caches, leftovers, storage int64
	for _, f := range m.result.Items {
		if f.Kind == "Cache" {
			caches += f.Size
		} else if f.Kind == "Leftover" {
			leftovers += f.Size
		} else if f.Kind == "Storage" {
			storage += f.Size
		}
	}
	header = append(header, fmt.Sprintf("Caches %s  ·  Suspected leftovers %s  ·  Storage %s", human(caches), human(leftovers), human(storage)))
	tabs := []string{"All", "Caches", "Leftovers", "Storage"}
	for i, t := range tabs {
		if i == m.tab {
			tabs[i] = accent.Render("[" + t + "]")
		}
	}
	gap := "   "
	if width < 60 {
		gap = " "
	}
	header = append(header, strings.Join(tabs, gap)+muted.Render(fmt.Sprintf("   %d warnings [w]", len(m.result.Warnings))))
	header = append(header, m.filterLine(width))
	for _, row := range header {
		add(row)
	}
	if m.directory != nil {
		rule("In: " + safe(m.directory.Path) + "  [Backspace up]")
	} else if m.tab == 3 {
		rule("Danger: unverified data · sizes include nested caches")
	} else {
		rule("")
	}
	rows := m.pageRows()
	items := m.visible()
	if m.scanning || m.loading {
		if m.loading {
			add(accent.Render("Reading directory" + strings.Repeat(".", m.frame%4)))
			add(muted.Render("Sizing children; q cancels."))
		} else {
			add(accent.Render("Scanning" + strings.Repeat(".", m.frame%4)))
			add(muted.Render("Library and installed apps; q cancels."))
		}
		for i := 2; i < rows; i++ {
			add("")
		}
	} else if m.showWarnings {
		start := max(0, m.cursor-rows+1)
		for i := 0; i < rows; i++ {
			idx := start + i
			if idx >= len(m.result.Warnings) {
				if i == 0 {
					add(muted.Render("No scan warnings."))
				} else {
					add("")
				}
				continue
			}
			text := safe(m.result.Warnings[idx])
			if idx == m.cursor {
				text = selected.Render("> " + m.scrollText(text, width-2))
			} else {
				text = warning.Render(text)
			}
			add(text)
		}
	} else {
		start := max(0, m.cursor-rows+1)
		for i := 0; i < rows; i++ {
			idx := start + i
			if idx >= len(items) {
				if i == 0 {
					if m.directory != nil {
						add(muted.Render("No listed children. Clear the filter or Backspace to go up."))
					} else {
						add(muted.Render("No findings here. Try another tab or clear the filter."))
					}
				} else {
					add("")
				}
				continue
			}
			f := items[idx]
			check := m.selectionMark(f.Path) + " "
			text := "  " + check + findingRow(f)
			if idx == m.cursor {
				text = selected.Render("> " + check + m.scrollText(findingRow(f), width-6))
			}
			add(text)
		}
	}
	rule("")
	if !m.showWarnings && len(items) > 0 {
		f := items[m.cursor]
		// Reveal and the confirmation show the unabridged path.
		path := safe(strings.TrimPrefix(f.Path, m.home) + "  [o: reveal]")
		add(muted.Render(m.scrollText("~"+path, width)))
		if f.Kind == "Storage" {
			add(danger.Render(m.scrollText(safe(f.Reason), width)))
		} else {
			add(warning.Render(m.scrollText(safe(f.Reason), width)))
		}
	} else if m.showWarnings && len(m.result.Warnings) > 0 {
		detail := ansi.Wrap(safe(m.result.Warnings[m.cursor]), width, "")
		lines := strings.Split(detail, "\n")
		add(warning.Render(lines[0]))
		if len(lines) > 1 {
			add(warning.Render(lines[1]))
		} else {
			add("")
		}
	} else {
		add(muted.Render("Read-only scan · No root access · No permanent deletion"))
		add("")
	}
	if m.statusError {
		add(danger.Render(safe(m.status)))
	} else {
		add(accent.Render(safe(m.status)))
	}
	add(muted.Render("Sizes are logical bytes, not guaranteed reclaimable disk space."))
	rule("")
	add(muted.Render("[space] check [b] batch [d] trash [a] Ask AI [enter] open [bksp] up [o] reveal"))
	add(muted.Render("[↑↓/PgUp/PgDn] move [←→] scroll [tab] tabs [/] filter [r] scan [?] About [q] quit"))
	return b.String()
}

func (m model) message(text string) string {
	return lipgloss.NewStyle().MarginLeft(2).Render(ansi.Wrap(text, max(1, m.width-4), "")) + "\n"
}

func (m model) confirmation() string {
	items := m.visible()
	if len(items) == 0 {
		return m.message(warning.Render("No item selected."))
	}
	f := items[m.cursor]
	title := "Move this one item to Trash?"
	disclaimer := warning.Render("Close its app first. Settings, offline files, and other valuable data may be inside. A leftover candidate is not proof of abandonment.")
	if f.Kind == "Storage" {
		title = "Danger: remove unverified storage?"
		disclaimer = danger.Render(storageWarning)
	}
	rule := divider.Render(strings.Repeat("─", max(1, m.width-4)))
	text := strings.Join([]string{
		danger.Bold(true).Render(title),
		muted.Render("Only this entry and its contents; parent and siblings stay."),
		rule,
		accent.Render(safe(f.Path)),
		"",
		muted.Render(safe(f.Kind)+" · ") + accent.Render(human(f.Size)),
		rule,
		disclaimer,
		"",
		muted.Render("Restore with Finder → Trash → Put Back."),
		rule,
		danger.Bold(true).Render("[y] Move to Trash") + "   " + accent.Render("[any other key] Cancel"),
	}, "\n")
	return m.message(text)
}

func main() {
	args := os.Args[1:]
	cleanMode := len(args) > 0 && args[0] == "clean"
	if cleanMode {
		args = args[1:]
	}
	flags := flag.NewFlagSet("wclean", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), "Usage: wclean [options]\n       wclean clean [--yes]\n\n")
		flags.PrintDefaults()
	}
	theme := flags.String("theme", "auto", "color theme: auto, light, dark")
	extra := flags.String("apps", "", "additional app search locations, separated by ':'")
	yes := flags.Bool("yes", false, "skip remembered-cache batch confirmation (clean command only)")
	showVersion := flags.Bool("version", false, "print version and exit")
	flags.Parse(args)
	if *showVersion {
		fmt.Println("wclean", version)
		return
	}
	if flags.NArg() != 0 || *yes && !cleanMode {
		flags.Usage()
		os.Exit(2)
	}
	if *theme != "auto" && *theme != "light" && *theme != "dark" {
		fmt.Fprintln(os.Stderr, "invalid --theme (use auto, light, dark)")
		os.Exit(2)
	}
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "wclean requires macOS")
		os.Exit(1)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		home, err = filepath.EvalSymlinks(home)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if os.Geteuid() == 0 {
		fmt.Fprintln(os.Stderr, "Run wclean as your normal user, not root.")
		os.Exit(1)
	}
	if *theme != "auto" {
		lipgloss.SetHasDarkBackground(*theme == "dark")
	}
	roots := []string{"/Applications", "/System/Applications", "/System/Library/CoreServices"}
	userApps := filepath.Join(home, "Applications")
	if _, err := os.Lstat(userApps); !os.IsNotExist(err) {
		roots = append(roots, userApps)
	}
	for _, root := range filepath.SplitList(*extra) {
		absolute, e := filepath.Abs(root)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(2)
		}
		roots = append(roots, absolute)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if cleanMode {
		cleanCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		if err := cleanRemembered(cleanCtx, home, os.Stdin, os.Stdout, *yes); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	remembered, memoryErr := loadMemory(home)
	m := model{ctx: ctx, home: home, roots: roots, scanning: true, remembered: remembered, memoryError: memoryErr}
	if _, err = tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
