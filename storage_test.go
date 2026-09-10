package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStorageDiscoveryAndDangerConfirmation(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]int{
		"Library/Developer/Xcode/DerivedData/project/build.o":                                                             5,
		"Library/Developer/Xcode/DocumentationCache/download":                                                             3,
		"Library/Developer/Xcode/Archives/release.xcarchive/Info.plist":                                                   11,
		"Library/Developer/Xcode/Products/app":                                                                            3,
		"Library/Developer/Xcode/UserData/settings":                                                                       7,
		"Library/Developer/Xcode/iOS DeviceSupport/symbols":                                                               4,
		"Library/Developer/Xcode/macOS DeviceSupport/symbols":                                                             4,
		"Library/Developer/Xcode/iOS Device Logs/log":                                                                     4,
		"Library/Containers/com.example.editor/Data/Library/Caches/data":                                                  13,
		"Library/Containers/com.example.editor/Data/Documents/keep":                                                       17,
		"Library/Containers/com.apple.CoreDevice.CoreDeviceService/Data/Library/Caches/AppInstallationBinaryDeltas/delta": 19,
		"Library/Containers/com.apple.mediaanalysisd/Data/Library/Caches/model":                                           23,
		"Library/Containers/unknown-uuid/Data/Library/Caches/data":                                                        31,
		"Library/Group Containers/group.com.example.shared/Library/Caches/offline":                                        29,
		"Library/Logs/app.log": 2,
		".konan/config":        2, ".lldb/config": 2, ".m2/settings.xml": 2, ".gradle/gradle.properties": 2,
		"Library/Caches/kitty/keep": 100,
	}
	for path, size := range files {
		writeTestFile(t, filepath.Join(home, path), strings.Repeat("x", size))
	}
	ctx := context.Background()
	result := scanLibrary(ctx, home, appInventory{Incomplete: true})
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
	paths := map[string]finding{}
	var caches int64
	for _, f := range result.Items {
		if _, exists := paths[f.Path]; exists {
			t.Fatalf("duplicate finding: %s", f.Path)
		}
		paths[f.Path] = f
		if f.Kind == "Cache" {
			caches += f.Size
			if err := validateRemoval(ctx, home, nil, f); err != nil {
				t.Fatal(err)
			}
		} else {
			if f.Kind != "Storage" {
				t.Fatalf("storage misclassified: %+v", f)
			}
			if _, err := validateLocation(home, f); err != nil {
				t.Fatal(err)
			}
			if err := validateRemoval(ctx, home, nil, f); err != nil {
				t.Fatalf("storage root should be removable after confirmation: %v", err)
			}
			if !strings.Contains(findingRow(f), "[danger]") {
				t.Fatal("storage row lacks danger marker")
			}
		}
	}
	if caches != 21 {
		t.Fatalf("cache total includes protected data or duplicates: %d", caches)
	}
	for _, relative := range []string{"Library/Developer/Xcode/Archives", "Library/Developer/Xcode/iOS DeviceSupport", "Library/Developer/Xcode/macOS DeviceSupport", "Library/Developer/Xcode/iOS Device Logs", "Library/Containers/com.apple.CoreDevice.CoreDeviceService", "Library/Containers/com.apple.mediaanalysisd", "Library/Group Containers/group.com.example.shared", "Library/Logs", ".konan", ".lldb", ".m2", ".gradle"} {
		f, ok := paths[filepath.Join(home, relative)]
		if !ok || f.Kind != "Storage" {
			t.Fatalf("missing inspection path: %s", relative)
		}
	}
	if _, ok := paths[filepath.Join(home, "Library/Caches/kitty")]; ok {
		t.Fatal("script's Kitty exclusion was lost")
	}
	// Unverified data remains Storage at every depth, but can be deliberately removed.
	for _, id := range []string{"com.apple.CoreDevice.CoreDeviceService", "com.apple.mediaanalysisd", "unknown-uuid"} {
		f := paths[filepath.Join(home, "Library/Containers", id)]
		for _, name := range []string{"Data", "Library", "Caches"} {
			_, children, err := readDirectory(ctx, home, f)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, child := range children.Items {
				if filepath.Base(child.Path) == name {
					f = child
					found = true
					break
				}
			}
			if !found || f.Kind != "Storage" {
				t.Fatalf("unverified data was misclassified: %s", id)
			}
			if err := validateRemoval(ctx, home, nil, f); err != nil {
				t.Fatalf("nested storage removal blocked: %v", err)
			}
		}
		forged := f
		forged.Kind = "Cache"
		if validateRemoval(ctx, home, nil, forged) == nil {
			t.Fatal("changing category bypassed the removal boundary")
		}
	}
	// A vetted cache still retains its separate classification within Storage.
	f := paths[filepath.Join(home, "Library/Containers/com.example.editor")]
	var dir finding
	var children scanResult
	for _, name := range []string{"Data", "Library", "Caches"} {
		dir, children, err = readDirectory(ctx, home, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, child := range children.Items {
			if filepath.Base(child.Path) == name {
				f = child
				break
			}
		}
	}
	if f.Kind != "Cache" || validateRemoval(ctx, home, nil, f) != nil {
		t.Fatal("vetted cache stayed blocked inside Storage")
	}
	m := model{home: home, tab: 3, directory: &dir, children: children.Items, result: result, width: 90, height: 28}
	if len(m.visible()) != 1 || m.visible()[0].Kind != "Cache" {
		t.Fatal("Storage tab hid a browsed cache")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if !updated.(model).confirm {
		t.Fatal("vetted cache cannot be confirmed")
	}
	m.directory = nil
	m.children = nil
	if !strings.Contains(m.View(), "Suspected leftovers 0 B") {
		t.Fatal("storage bytes leaked into leftover total")
	}
	if !strings.Contains(m.View(), "Storage 175 B") {
		t.Fatal("header must show the total of Storage findings")
	}
	for _, item := range m.visible() {
		if item.Kind != "Storage" {
			t.Fatal("Storage tab contains cleanup findings")
		}
	}
	m.width = max(90, len(m.visible()[0].Path)+10)
	m.height = 40
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	confirmed := updated.(model)
	if cmd != nil || !confirmed.confirm || confirmed.busy {
		t.Fatal("storage removal must require confirmation")
	}
	if !strings.Contains(confirmed.confirmation(), "Danger: remove unverified storage?") || !strings.Contains(confirmed.confirmation(), m.visible()[0].Path) {
		t.Fatal("danger confirmation must show the risk and full path")
	}
	cancelled, cmd := confirmed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || cancelled.(model).confirm || cancelled.(model).busy {
		t.Fatal("Enter must cancel storage removal")
	}
	approved, cmd := confirmed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil || !approved.(model).busy {
		t.Fatal("explicit confirmation did not allow storage removal")
	}
	// Do not execute the returned command: tests never invoke the real Trash.
	partial := m.visible()[0]
	partial.Incomplete = true
	if validateRemoval(ctx, home, nil, partial) == nil {
		t.Fatal("danger confirmation bypassed incomplete-scan protection")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if updated.(model).tab != 0 {
		t.Fatal("Tab must wrap after Storage")
	}
	m.tab = 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if updated.(model).tab != 3 {
		t.Fatal("Shift+Tab must reach Storage")
	}
}

func TestStorageTotalsAfterCacheRemoval(t *testing.T) {
	for _, childRemoval := range []bool{false, true} {
		home, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		container := filepath.Join(home, "Library/Containers/com.example.editor")
		cache := filepath.Join(container, "Data/Library/Caches")
		writeTestFile(t, filepath.Join(cache, "data"), "1234567")
		writeTestFile(t, filepath.Join(container, "Data/Documents/keep"), "123")
		result := scanLibrary(context.Background(), home, appInventory{})
		m := model{home: home, result: result, tab: 1}
		target := m.visible()[0]
		if childRemoval {
			dir, children, e := readDirectory(context.Background(), home, target)
			if e != nil {
				t.Fatal(e)
			}
			m.directory = &dir
			m.children = children.Items
			target = m.visible()[0]
		}
		if err := os.Rename(target.Path, filepath.Join(home, "simulated-trash")); err != nil {
			t.Fatal(err)
		}
		updated, cmd := m.Update(trashMsg{path: target.Path})
		m = updated.(model)
		if cmd != nil || m.scanning || m.loading {
			t.Fatal("removal started a rescan")
		}
		for _, f := range m.result.Items {
			if f.Kind == "Storage" && f.Size != 3 {
				t.Fatalf("inclusive storage size wasn't updated: %d", f.Size)
			}
			if f.Kind == "Cache" && f.Size != 0 {
				t.Fatalf("cache bytes weren't subtracted: %d", f.Size)
			}
		}
		if _, err := os.Stat(filepath.Join(container, "Data/Documents/keep")); err != nil {
			t.Fatal("non-cache data changed")
		}
	}
}

func TestStorageRemovalDropsNestedFindings(t *testing.T) {
	for _, inside := range []bool{false, true} {
		home, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		container := filepath.Join(home, "Library/Containers/com.example.editor")
		writeTestFile(t, filepath.Join(container, "Data/Library/Caches/data"), "1234567")
		writeTestFile(t, filepath.Join(container, "Data/Documents/data"), "123")
		writeTestFile(t, filepath.Join(container, "metadata"), "12")
		writeTestFile(t, filepath.Join(container+".backup", "keep"), "12345")
		result := scanLibrary(context.Background(), home, appInventory{})
		m := model{home: home, result: result, tab: 3}
		target := m.visible()[0]
		if inside {
			dir, children, err := readDirectory(context.Background(), home, target)
			if err != nil {
				t.Fatal(err)
			}
			m.directory, m.children = &dir, children.Items
			target = m.visible()[0]
		}
		if err := validateRemoval(context.Background(), home, nil, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(target.Path, filepath.Join(home, "simulated-trash")); err != nil {
			t.Fatal(err)
		}
		updated, cmd := m.Update(trashMsg{path: target.Path})
		m = updated.(model)
		if cmd != nil || m.scanning || m.loading {
			t.Fatal("storage removal triggered a scan")
		}
		var total int64
		for _, f := range m.result.Items {
			if f.Kind == "Cache" || f.Path == target.Path || strings.HasPrefix(f.Path, target.Path+string(filepath.Separator)) {
				t.Fatal("removed descendant is still listed")
			}
			total += f.Size
		}
		want := int64(5)
		if inside {
			want += 2
		}
		if total != want {
			t.Fatalf("remaining storage = %d, want %d", total, want)
		}
		if _, err := os.Stat(filepath.Join(container+".backup", "keep")); err != nil {
			t.Fatal("sibling-prefix directory was affected")
		}
		if inside && (len(m.children) != 1 || m.directory.Size != 2) {
			t.Fatal("parent listing or total is stale")
		}
	}
}

func TestStorageSymlinks(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, "Library/Containers/com.example.editor")
	writeTestFile(t, filepath.Join(home, "outside/Library/Caches/data"), "outside")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "outside"), filepath.Join(base, "Data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "outside"), filepath.Join(home, ".gradle")); err != nil {
		t.Fatal(err)
	}
	result := scanLibrary(context.Background(), home, appInventory{})
	if len(result.Items) != 1 || result.Items[0].Kind != "Storage" || result.Items[0].Size != 0 || len(result.Warnings) == 0 {
		t.Fatalf("followed a storage/cache symlink: %+v", result)
	}
}
