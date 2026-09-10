package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestAIMetadataOnly(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "Library/Caches/example")
	for i := 0; i < 24; i++ {
		writeTestFile(t, filepath.Join(path, string(rune('a'+i))), "PRIVATE FILE CONTENTS")
	}
	writeTestFile(t, filepath.Join(path, "nested/deeper-secret"), "ALSO PRIVATE")
	f := scanLibrary(context.Background(), home, appInventory{}).Items[0]
	prompt, err := prepareAIPrompt(context.Background(), home, f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, home) || strings.Contains(prompt, "PRIVATE") || strings.Contains(prompt, "deeper-secret") {
		t.Fatal("prompt included private contents, home identity, or recursive data")
	}
	_, data, ok := strings.Cut(prompt, "Metadata:\n")
	if !ok {
		t.Fatal("metadata missing")
	}
	var metadata struct {
		Path     string              `json:"path"`
		Children []map[string]string `json:"sample_children_not_recursive"`
		More     bool                `json:"more_entries_not_shown"`
	}
	if err := json.Unmarshal([]byte(data), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Path != "~/Library/Caches/example" || len(metadata.Children) != 20 || !metadata.More {
		t.Fatalf("incorrect bounded metadata: %+v", metadata)
	}
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+"-old", path); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareAIPrompt(context.Background(), home, f); err == nil {
		t.Fatal("AI inspection followed a substituted symlink")
	}
}

func TestAIClients(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if len(installedAIClients()) != 0 {
		t.Fatal("found nonexistent client")
	}
	script := `#!/bin/sh
case "$PWD" in */wclean-ai-*) ;; *) exit 5 ;; esac
IFS= read -r prompt
[ "$prompt" = 'test prompt' ] || exit 6
case "$0" in
 *codex)
  while [ "$#" -gt 0 ]; do
   if [ "$1" = '--output-last-message' ]; then shift; printf 'answer\n' > "$1"; break; fi
   shift
  done
  printf 'progress noise\n'
  ;;
 *) printf '\033[31manswer\033[0m\n' ;;
esac
`
	for _, name := range []string{"claude", "codex", "pi"} {
		path := filepath.Join(dir, name)
		writeTestFile(t, path, script)
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	clients := installedAIClients()
	if len(clients) != 3 {
		t.Fatal("installed CLI detection failed")
	}
	for _, client := range clients {
		answer, err := askAI(context.Background(), client, "test prompt\n")
		if err != nil || answer != "answer" {
			t.Fatalf("%s: answer=%q, err=%v", client.name, answer, err)
		}
		args, err := aiArguments(client.name, "answer.txt")
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		switch client.name {
		case "claude":
			if !strings.Contains(joined, "--restricted") || !strings.Contains(joined, "--strict-mcp-config") {
				t.Fatal("Claude restrictions missing")
			}
		case "codex":
			if !strings.Contains(joined, "--sandbox read-only") || !strings.Contains(joined, "--disable shell_tool") || !strings.Contains(joined, "--ignore-user-config") {
				t.Fatal("Codex restrictions missing")
			}
		case "pi":
			if !strings.Contains(joined, "--no-tools") || !strings.Contains(joined, "--no-extensions") || !strings.Contains(joined, "--no-context-files") {
				t.Fatal("pi restrictions missing")
			}
		}
	}
	writeTestFile(t, clients[0].path, "#!/bin/sh\necho 'login required' >&2\nexit 7\n")
	if _, err := askAI(context.Background(), clients[0], "test"); err == nil || !strings.Contains(err.Error(), "login required") {
		t.Fatal("provider errors were hidden")
	}
	writeTestFile(t, clients[0].path, "#!/bin/sh\nexec /bin/sleep 10\n")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := askAI(ctx, clients[0], "test"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request cancellation failed: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancelled client was not stopped promptly")
	}
	output := &cappedOutput{limit: 8}
	if n, err := output.Write([]byte("0123456789")); err != nil || n != 10 || output.Len() != 8 || !output.truncated {
		t.Fatal("unbounded provider output")
	}
	if got := cleanAIText("one\n\x1b[31mtwo\x1b[0m\x00"); strings.Contains(got, "\x1b") || !strings.Contains(got, "\n") || strings.Contains(got, "\x00") {
		t.Fatal("unsafe or flattened provider text")
	}
}

func TestAIDialogFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := model{ctx: ctx, width: 90, height: 28, result: scanResult{Items: []finding{{Path: "/selection"}}}}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(model)
	if cmd == nil || m.ai == nil || m.ai.stage != aiPreparing {
		t.Fatal("AI action did not open a preview")
	}
	id := m.ai.id
	updated, _ = m.Update(aiPreparedMsg{id: id, clients: []aiClient{{"claude", "unused"}, {"pi", "unused"}}, prompt: "preview"})
	m = updated.(model)
	if m.ai.stage != aiPreview {
		t.Fatal("provider started before confirmation")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(model)
	if m.confirm || m.busy {
		t.Fatal("modal key escaped into deletion")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(model)
	if m.ai.selected != 1 {
		t.Fatal("provider picker did not move")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd == nil || m.ai.stage != aiWaiting || m.aiLast != "pi" {
		t.Fatal("explicit send did not start the chosen client")
	}
	requestContext := m.ai.ctx
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.ai != nil || requestContext.Err() == nil {
		t.Fatal("closing the popup did not cancel the request")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(model)
	updated, _ = m.Update(aiReplyMsg{id: id, text: "stale reply"})
	m = updated.(model)
	if m.ai.stage != aiPreparing {
		t.Fatal("stale response replaced a new request")
	}
	m.ai.stage = aiAnswer
	m.ai.answer = strings.Repeat("long response line\n", 100)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(model)
	if m.ai.scroll == 0 {
		t.Fatal("AI response cannot be scrolled")
	}
	for _, size := range [][2]int{{48, 18}, {90, 28}, {140, 50}} {
		m.width, m.height = size[0], size[1]
		view := ansi.Strip(m.View())
		if lipgloss.Width(view) > m.width || lipgloss.Height(view) > m.height {
			t.Fatalf("popup exceeds %dx%d", m.width, m.height)
		}
		lines := strings.Split(view, "\n")
		top, bottom, left := -1, -1, -1
		for i, line := range lines {
			if p := strings.Index(line, "╭"); p >= 0 {
				top, left = i, p
			}
			if strings.Contains(line, "╰") {
				bottom = i
			}
		}
		if top < 0 || bottom < 0 || left < 0 {
			t.Fatal("popup border missing")
		}
		if delta := top - (m.height - bottom - 1); delta < -1 || delta > 1 {
			t.Fatal("popup is not vertically centered")
		}
		if delta := left - (m.width - left - min(100, m.width-4)); delta < -1 || delta > 1 {
			t.Fatal("popup is not horizontally centered")
		}
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if m.ai != nil || len(m.result.Items) != 1 {
		t.Fatal("closing explanation changed findings")
	}
}
