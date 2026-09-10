package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBrowseAndRemoveChild(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	apps := filepath.Join(home, "Applications")
	if err := os.Mkdir(apps, 0700); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, "Library/Caches/example")
	writeTestFile(t, filepath.Join(base, "nested/remove.txt"), "123456789")
	writeTestFile(t, filepath.Join(base, "nested/keep.txt"), "12")
	writeTestFile(t, filepath.Join(base, ".hidden"), "123")
	if err := os.Mkdir(filepath.Join(base, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(home, "outside/data"), "never follow")
	if err := os.Symlink(filepath.Join(home, "outside"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	result := scan(ctx, home, []string{apps})
	if len(result.Items) != 1 || result.Items[0].Size != 14 {
		t.Fatalf("root size: %+v", result)
	}
	root := result.Items[0]
	m := model{ctx: ctx, home: home, roots: []string{apps}, result: result, width: max(90, len(base)+40), height: 28}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if !m.loading || m.confirm {
		t.Fatal("Enter must load a directory, not confirm deletion")
	}
	updated, _ = m.Update(m.directoryCmd(root, "")())
	m = updated.(model)
	if m.directory == nil || m.directory.Path != base || len(m.visible()) != 3 {
		t.Fatalf("bad directory listing: %+v", m.children)
	}
	if m.visible()[0].Size != 11 || !strings.HasSuffix(findingRow(m.visible()[0]), "nested/") {
		t.Fatal("children must be sized and directories marked")
	}
	if !strings.Contains(m.View(), "In: "+base) || len(m.result.Warnings) != 1 {
		t.Fatal("missing breadcrumb or skipped-symlink warning")
	}
	hidden := m.children[1]
	if filepath.Base(hidden.Path) != ".hidden" || validateFinding(home, hidden) != nil {
		t.Fatal("hidden child must be independently removable")
	}
	nested := m.visible()[0]
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	updated, _ = m.Update(m.directoryCmd(nested, "")())
	m = updated.(model)
	target := m.visible()[0]
	if target.origin().Path != base || filepath.Base(target.Path) != "remove.txt" {
		t.Fatal("child lost its detected root")
	}
	if err := validateRemoval(ctx, home, []string{apps}, target); err != nil {
		t.Fatal(err)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = updated.(model)
	if !m.confirm || !strings.Contains(m.confirmation(), target.Path) {
		t.Fatal("confirmation must identify the child")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if m.confirm || m.busy {
		t.Fatal("Enter must cancel deletion")
	}
	// Simulate a successful move, never invoke Finder or touch the real Trash.
	if err := os.Rename(target.Path, filepath.Join(home, "simulated-trash")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(home, "Library/Caches/new-cache/data"), "not discovered automatically")
	updated, cmd := m.Update(trashMsg{path: target.Path})
	m = updated.(model)
	if m.scanning || m.loading || cmd != nil {
		t.Fatal("child removal must not start a scan or reload command")
	}
	if len(m.result.Items) != 1 || m.directory.Path != nested.Path || m.directory.Size != 2 || m.directory.parent.Size != 5 {
		t.Fatal("directory position or ancestor totals were not preserved")
	}
	if err := validateFinding(home, *m.directory); err != nil {
		t.Fatalf("current directory metadata was not refreshed: %v", err)
	}
	if err := validateFinding(home, m.result.Items[0]); err != nil {
		t.Fatalf("root metadata was not refreshed: %v", err)
	}
	if m.result.Items[0].Size != 5 || len(m.visible()) != 1 || filepath.Base(m.visible()[0].Path) != "keep.txt" {
		t.Fatal("child listing or totals not refreshed")
	}
	if _, err := os.Stat(filepath.Join(base, "nested/keep.txt")); err != nil {
		t.Fatal("sibling was affected")
	}
	if _, err := os.Stat(filepath.Join(base, ".hidden")); err != nil {
		t.Fatal("parent sibling was affected")
	}
	// Backspace reloads the parent and focuses the directory we just left.
	parent := *m.directory.parent
	focus := m.directory.Path
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(model)
	if !m.loading {
		t.Fatal("Backspace must load the parent")
	}
	updated, _ = m.Update(m.directoryCmd(parent, focus)())
	m = updated.(model)
	if m.visible()[m.cursor].Path != focus {
		t.Fatal("parent selection was not restored")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(model)
	if m.directory != nil || m.visible()[m.cursor].Path != root.Path {
		t.Fatal("Backspace must stop at detected findings")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if updated.(model).directory != nil {
		t.Fatal("escaped findings root")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = updated.(model)
	if !m.scanning || cmd == nil {
		t.Fatal("manual rescan must remain available")
	}
	updated, _ = m.Update(m.scanCmd())
	refreshed := updated.(model)
	if len(refreshed.result.Items) != 3 {
		t.Fatal("rescan must include two caches and the missing remembered child")
	}
	if refreshed.selectionMark(target.Path) != "[x]" {
		t.Fatal("removed cache was not remembered for the next batch")
	}
}

func TestRemovalUpdatesFilteredListing(t *testing.T) {
	m := model{tab: 1, query: "cache", cursor: 1, busy: true, result: scanResult{
		Items:    []finding{{Path: "/cache-big", Kind: "Cache", Size: 9}, {Path: "/cache-small", Kind: "Cache", Size: 4}, {Path: "/leftover", Kind: "Leftover", Size: 15}},
		Warnings: []string{"existing warning"},
	}}
	updated, cmd := m.Update(trashMsg{path: "/cache-small", err: errors.New("denied")})
	m = updated.(model)
	if cmd != nil || len(m.result.Items) != 3 || m.visible()[1].Size != 4 {
		t.Fatal("failed removal changed findings")
	}
	updated, cmd = m.Update(trashMsg{path: "/cache-small"})
	m = updated.(model)
	if cmd != nil || m.scanning || m.loading || m.busy {
		t.Fatal("removal scheduled a scan")
	}
	if m.query != "cache" || m.tab != 1 || m.cursor != 0 || len(m.visible()) != 1 || m.visible()[0].Size != 9 {
		t.Fatal("filter, cursor, or remaining total changed")
	}
	if len(m.result.Items) != 2 || m.result.Items[0].Size != 15 || len(m.result.Warnings) != 1 {
		t.Fatal("unrelated findings or warnings changed")
	}
	updated, cmd = m.Update(trashMsg{path: "/cache-small"})
	m = updated.(model)
	if cmd != nil || len(m.result.Items) != 2 || m.visible()[0].Size != 9 {
		t.Fatal("duplicate completion changed totals")
	}
	updated, cmd = m.Update(trashMsg{path: "/cache-big"})
	m = updated.(model)
	if cmd != nil || len(m.visible()) != 0 || m.cursor != 0 {
		t.Fatal("empty filtered view has an invalid cursor")
	}
}

func TestRemovalDoesNotTrustReplacedAncestor(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, "Library/Caches/example")
	writeTestFile(t, filepath.Join(base, "remove"), "1234")
	writeTestFile(t, filepath.Join(base, "keep"), "12")
	result := scanLibrary(context.Background(), home, appInventory{})
	dir, children, err := readDirectory(context.Background(), home, result.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	target := children.Items[0]
	if err := os.Rename(target.Path, filepath.Join(home, "simulated-trash")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(base, base+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base+"-old", base); err != nil {
		t.Fatal(err)
	}
	m := model{home: home, result: result, directory: &dir, children: children.Items}
	updated, cmd := m.Update(trashMsg{path: target.Path})
	m = updated.(model)
	if cmd != nil || m.scanning || m.loading {
		t.Fatal("metadata failure started a scan")
	}
	if len(m.children) != 1 || m.result.Items[0].Size != 2 || !m.result.Items[0].Incomplete || len(m.result.Warnings) == 0 {
		t.Fatal("successful move or metadata failure not reflected")
	}
	if validateFinding(home, m.result.Items[0]) == nil || validateFinding(home, m.children[0]) == nil {
		t.Fatal("replaced ancestor was authorized by metadata refresh")
	}
}

func TestBrowseBoundary(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, "Library/Caches/example")
	writeTestFile(t, filepath.Join(base, "nested/file"), "123")
	root := scanLibrary(context.Background(), home, appInventory{}).Items[0]
	root.Incomplete = true // A readable child can be reviewed even if a sibling was unreadable.
	_, children, err := readDirectory(context.Background(), home, root)
	if err != nil {
		t.Fatal(err)
	}
	nested := children.Items[0]
	_, children, err = readDirectory(context.Background(), home, nested)
	if err != nil {
		t.Fatal(err)
	}
	child := children.Items[0]
	if err := validateFinding(home, child); err != nil {
		t.Fatal(err)
	}
	bad := child
	bad.Incomplete = true
	if validateFinding(home, bad) == nil {
		t.Fatal("partial child accepted for removal")
	}
	bad = child
	bad.parent = nil
	if validateFinding(home, bad) == nil {
		t.Fatal("unanchored nested path accepted")
	}
	outside := filepath.Join(home, "Library/Caches/example-other/file")
	writeTestFile(t, outside, "123")
	bad = child
	bad.Path = outside
	bad.info, _ = os.Lstat(outside)
	if validateFinding(home, bad) == nil {
		t.Fatal("sibling-prefix escape accepted")
	}
	// Keep the child's inode but replace its ancestor: validating only the child is insufficient.
	moved := nested.Path + "-old"
	if err := os.Rename(nested.Path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(nested.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(moved, "file"), child.Path); err != nil {
		t.Fatal(err)
	}
	if validateFinding(home, child) == nil {
		t.Fatal("replaced ancestor accepted")
	}
	if err := os.Rename(nested.Path, nested.Path+"-replacement"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nested.Path+"-replacement", nested.Path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readDirectory(context.Background(), home, nested); err == nil {
		t.Fatal("symlinked directory followed")
	}
	if validateFinding(home, child) == nil {
		t.Fatal("symlinked ancestor accepted for removal")
	}
}
