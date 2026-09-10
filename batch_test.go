package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestRememberMissingCachesAndForget(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "Library/Caches/example/child")
	writeTestFile(t, path, "old")
	f, err := resolveCache(context.Background(), home, path)
	if err != nil {
		t.Fatal(err)
	}
	m := model{home: home, result: scanResult{Items: []finding{f}}}
	if err := os.Rename(path, filepath.Join(home, "simulated-trash")); err != nil {
		t.Fatal(err)
	}
	updated, cmd := m.Update(trashMsg{path: path})
	m = updated.(model)
	if cmd != nil || !m.remembered[path] {
		t.Fatal("successful cache removal was not remembered locally")
	}
	memory, err := loadMemory(home)
	if err != nil || !memory[path] {
		t.Fatalf("remembered path not saved: %v", err)
	}
	before, err := os.ReadFile(memoryPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := cleanRemembered(context.Background(), home, strings.NewReader(""), &output, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "MISSING") {
		t.Fatal("missing cache was not checked")
	}
	after, _ := os.ReadFile(memoryPath(home))
	if !bytes.Equal(before, after) {
		t.Fatal("missing cache was forgotten by batch cleanup")
	}
	// A regenerated entry is checked again; the remembered path is not a stale inode.
	writeTestFile(t, path, "regenerated")
	plan := prepareBatch(context.Background(), home, m.selectedItems())
	if len(plan) != 1 || plan[0].missing || plan[0].err != nil || plan[0].finding.Size != 11 {
		t.Fatalf("regenerated cache not resolved: %+v", plan)
	}
	output.Reset()
	if err := cleanRemembered(context.Background(), home, strings.NewReader("n\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("CLI removed a cache without confirmation")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	refreshed := scanLibrary(context.Background(), home, appInventory{})
	m.addMissingMemory(&refreshed)
	m.result = refreshed
	missingIndex := -1
	for i, item := range m.visible() {
		if item.Path == path && item.Missing {
			missingIndex = i
		}
	}
	if missingIndex < 0 {
		t.Fatal("missing remembered entry cannot be unchecked in findings")
	}
	m.cursor = missingIndex
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(model)
	memory, err = loadMemory(home)
	if err != nil || len(memory) != 0 || m.selectionMark(path) != "[ ]" {
		t.Fatal("unchecking did not forget the missing path")
	}
	info, err := os.Stat(memoryPath(home))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("preferences must be private")
	}
}

func TestRememberingRetriesFailedSave(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "Library/Caches/example")
	writeTestFile(t, path, "cache")
	f, err := resolveCache(context.Background(), home, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(memoryPath(home), 0700); err != nil {
		t.Fatal(err)
	}
	m := model{home: home}
	if m.rememberCache(f) == nil || !m.memoryDirty {
		t.Fatal("failed save was treated as durable")
	}
	if err := os.Remove(memoryPath(home)); err != nil {
		t.Fatal(err)
	}
	if err := m.rememberCache(f); err != nil || m.memoryDirty {
		t.Fatal("remembering did not retry the failed save")
	}
	memory, err := loadMemory(home)
	if err != nil || !memory[path] {
		t.Fatal("retry did not persist the cache")
	}
}

func TestBatchSelectionsAndSafety(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(home, "Library/Caches/foo")
	child := filepath.Join(parent, "child")
	sibling := filepath.Join(home, "Library/Caches/foo-bar")
	writeTestFile(t, child, "123")
	writeTestFile(t, sibling, "12345")
	selected := []finding{{Path: child, Kind: "Cache"}, {Path: parent, Kind: "Cache"}, {Path: sibling, Kind: "Cache"}, {Path: parent, Kind: "Cache"}}
	plan := prepareBatch(context.Background(), home, selected)
	if len(plan) != 2 || plan[0].finding.Path != parent || plan[1].finding.Path != sibling {
		t.Fatalf("overlapping selections not deduplicated: %+v", plan)
	}
	m := model{home: home, remembered: map[string]bool{child: true}, checked: map[string]finding{}}
	if m.selectionMark(parent) != "[~]" || m.selectionMark(child) != "[x]" || m.selectionMark(sibling) != "[ ]" {
		t.Fatal("incorrect tree checkbox state")
	}
	root, err := resolveCache(context.Background(), home, parent)
	if err != nil {
		t.Fatal(err)
	}
	m.checked[parent] = root
	if m.selectionMark(filepath.Join(parent, "other")) != "[x]" {
		t.Fatal("ancestor selection must cover children")
	}
	m.toggleChecked(finding{Path: child})
	if len(m.remembered) != 0 || len(m.checked) != 0 {
		t.Fatal("unchecking a child left an enclosing destructive selection active")
	}
	archive := filepath.Join(home, "Library/Developer/Xcode/Archives")
	writeTestFile(t, filepath.Join(archive, "keep"), "archive")
	archiveInfo, err := os.Lstat(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.rememberCache(finding{Path: archive, Kind: "Storage", info: archiveInfo}); err != nil || len(m.remembered) != 0 {
		t.Fatal("Storage removal became an automatic cache rule")
	}
	for _, bad := range []string{home, filepath.Join(home, "Library/Caches"), filepath.Join(home, "Library/Caches/kitty"), filepath.Join(home, "Library/Developer/Xcode/Archives"), filepath.Join(home, ".m2"), parent + "/../foo-bar"} {
		if _, err := cacheRootFor(home, bad); err == nil {
			t.Fatalf("unsafe remembered scope: %s", bad)
		}
	}
	if err := os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent+"-old", parent); err != nil {
		t.Fatal(err)
	}
	plan = prepareBatch(context.Background(), home, []finding{{Path: child, Kind: "Cache"}})
	if len(plan) != 1 || plan[0].err == nil || plan[0].missing {
		t.Fatal("symlinked remembered path was accepted or forgotten")
	}
	// Malformed/hostile preferences must never be overwritten with an empty set.
	data, _ := json.Marshal([]string{filepath.Join(home, ".m2")})
	writeTestFile(t, memoryPath(home), string(data))
	_, loadErr := loadMemory(home)
	if loadErr == nil {
		t.Fatal("non-cache preferences accepted")
	}
	m.memoryError = loadErr
	if err := m.persistMemory(); err == nil {
		t.Fatal("corrupt preferences were overwritten")
	}
	after, _ := os.ReadFile(memoryPath(home))
	if !bytes.Equal(data, after) {
		t.Fatal("existing preferences lost")
	}
	var out bytes.Buffer
	if err := cleanRemembered(context.Background(), home, strings.NewReader("y\n"), &out, true); err == nil {
		t.Fatal("CLI accepted hostile preferences")
	}
}

func TestBatchDialogProgress(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(home, "Library/Caches/first")
	second := filepath.Join(home, "Library/Caches/second")
	writeTestFile(t, first, "123")
	writeTestFile(t, second, "12345")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := scanLibrary(ctx, home, appInventory{})
	m := model{ctx: ctx, home: home, result: result, width: 90, height: 28, checked: map[string]finding{}}
	for _, f := range result.Items {
		m.checked[f.Path] = f
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = updated.(model)
	if cmd == nil || m.batch == nil || m.batch.stage != batchPreparing {
		t.Fatal("batch did not start with a preview")
	}
	id := m.batch.id
	updated, _ = m.Update(batchPreparedMsg{id: id, items: prepareBatch(ctx, home, m.selectedItems())})
	m = updated.(model)
	if m.batch.stage != batchPreview || m.busy {
		t.Fatal("preview started deleting")
	}
	if lipgloss.Width(m.View()) > m.width || lipgloss.Height(m.View()) > m.height {
		t.Fatal("batch popup overflows")
	}
	// Enter cancels; a new preview requires explicit y.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd != nil || m.batch != nil {
		t.Fatal("Enter must cancel batch removal")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = updated.(model)
	id = m.batch.id
	updated, _ = m.Update(batchPreparedMsg{id: id, items: prepareBatch(ctx, home, m.selectedItems())})
	m = updated.(model)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(model)
	if cmd == nil || !m.busy || m.batch.stage != batchRunning {
		t.Fatal("y did not begin batch")
	}
	// Simulate one native move, never execute a real Trash command in tests.
	target := m.batch.items[0].finding
	if err := os.Rename(target.Path, filepath.Join(home, "simulated-trash")); err != nil {
		t.Fatal(err)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(model)
	if !m.batch.stop || !m.busy {
		t.Fatal("stop must wait for the current item")
	}
	updated, cmd = m.Update(batchStepMsg{id: id, index: 0})
	m = updated.(model)
	if cmd != nil || m.busy || m.scanning || m.batch.stage != batchDone || m.batch.moved != 1 {
		t.Fatal("batch did not stop locally after current item")
	}
	if !m.remembered[target.Path] || len(m.result.Items) != 1 {
		t.Fatal("batch move was not remembered or removed from UI")
	}
	unchanged := m.batch.moved
	updated, _ = m.Update(batchStepMsg{id: id, index: 0})
	m = updated.(model)
	if m.batch.moved != unchanged {
		t.Fatal("duplicate completion was applied")
	}
	// Missing and failed entries report outcomes without pretending a removal succeeded.
	bctx, bcancel := context.WithCancel(ctx)
	defer bcancel()
	m.batch = &batchDialog{id: 99, stage: batchRunning, ctx: bctx, cancel: bcancel, items: []batchItem{{finding: finding{Path: first}}, {finding: finding{Path: second}}}}
	m.busy = true
	updated, _ = m.Update(batchStepMsg{id: 99, index: 0, missing: true})
	m = updated.(model)
	updated, _ = m.Update(batchStepMsg{id: 99, index: 1, err: errors.New("denied")})
	m = updated.(model)
	if m.batch.moved != 0 || m.batch.missing != 1 || m.batch.failures != 1 || m.busy {
		t.Fatal("partial batch outcomes were lost")
	}
}
