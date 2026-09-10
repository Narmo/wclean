package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestWclean(t *testing.T) {
	home := t.TempDir()
	// macOS temp directories may themselves be reached through /var symlinks.
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	put := func(relative, content string) string {
		t.Helper()
		path := filepath.Join(home, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cache := put("Library/Caches/com.example.live/data", "12345")
	put("Library/Preferences/com.example.gone.plist", "123")
	put("Library/Preferences/com.example.live.plist", "keep")
	put("Library/Preferences/com.apple.finder.plist", "keep")
	put("Library/Application Support/Human Name/data", "keep")
	outside := put("outside/data", "do not count")
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(cache), "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(home, "Library/Caches/redirect")); err != nil {
		t.Fatal(err)
	}
	result := scanLibrary(context.Background(), home, appInventory{IDs: map[string]bool{"com.example.live": true}})
	if len(result.Warnings) != 0 || len(result.Items) != 2 {
		t.Fatalf("unexpected scan: %+v", result)
	}
	if result.Items[0].Size != 5 || result.Items[1].Size != 3 || result.Items[1].Kind != "Leftover" {
		t.Fatalf("wrong sizes or categories: %+v", result.Items)
	}
	for _, f := range result.Items {
		if err := validateFinding(home, f); err != nil {
			t.Fatal(err)
		}
	}
	f := result.Items[1]
	if err := os.WriteFile(f.Path, []byte("changed contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if validateFinding(home, f) == nil {
		t.Fatal("accepted changed file")
	}
	f = result.Items[0]
	f.Incomplete = true
	if validateFinding(home, f) == nil {
		t.Fatal("accepted partial scan")
	}
	f = result.Items[0]
	f.Path = outside
	if validateFinding(home, f) == nil {
		t.Fatal("accepted outside path")
	}
	f = result.Items[0]
	old := filepath.Dir(f.Path)
	if err := os.Rename(old, old+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old+"-old", old); err != nil {
		t.Fatal(err)
	}
	if validateFinding(home, f) == nil {
		t.Fatal("accepted symlinked parent")
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old+"-old", old); err != nil {
		t.Fatal(err)
	}
	partial := scanLibrary(context.Background(), home, appInventory{Incomplete: true})
	if len(partial.Items) != 1 || partial.Items[0].Kind != "Cache" {
		t.Fatal("incomplete inventory must suppress leftovers")
	}
	if strings.Contains(safe("bad\x1b[31m\nname"), "\x1b") {
		t.Fatal("terminal escape survived")
	}
	m := model{ctx: context.Background(), home: home, result: result, width: 90, height: 28}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(model)
	if !m.confirm || m.busy {
		t.Fatal("delete must require confirmation")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.confirm || m.busy {
		t.Fatal("Enter must cancel, not delete")
	}
	for _, size := range [][2]int{{48, 18}, {90, 28}, {150, 45}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		plain := ansi.Strip(view)
		if !strings.HasPrefix(plain, "  wclean") || strings.ContainsAny(plain, "▀▄") {
			t.Fatal("main header must be text-only")
		}
		if strings.Count(view, strings.Repeat("─", m.width-4)) != 3 {
			t.Fatal("expected three full-width section dividers")
		}
		if lines := strings.Count(view, "\n"); lines > m.height {
			t.Fatalf("view overflows: %d > %d", lines, m.height)
		}
	}
}
