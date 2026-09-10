package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitAppCaches(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range cacheTargets {
		writeTestFile(t, filepath.Join(home, "Library", target.path, "data"), "1234")
	}
	protected := []string{
		"Application Support/Code/User/globalStorage",
		"Application Support/Code/Backups",
		"Application Support/Code/Service Worker/CacheStorage",
		"Application Support/discord/Local Storage",
		"Application Support/Claude/local-agent-mode-sessions",
		"Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/Service Worker/CacheStorage",
		"Containers/com.apple.CoreDevice.CoreDeviceService/Data/Library/Caches/AppInstallationBinaryDeltas",
		"Arduino15/packages",
	}
	for _, path := range protected {
		writeTestFile(t, filepath.Join(home, "Library", path, "keep"), "protected")
	}
	// Application cache rules still operate when leftover ownership is unknown.
	result := scanLibrary(context.Background(), home, appInventory{Incomplete: true})
	cached := model{result: result, tab: 1}
	cacheItems := cached.visible()
	if len(result.Warnings) != 0 || len(cacheItems) != len(cacheTargets) {
		t.Fatalf("unexpected cache scan: %+v", result)
	}
	var total int64
	for _, f := range cacheItems {
		if f.Kind != "Cache" || f.Label == "" || f.Size != 4 {
			t.Fatalf("bad cache finding: %+v", f)
		}
		total += f.Size
		if err := validateRemoval(context.Background(), home, nil, f); err != nil {
			t.Fatal(err)
		}
		_, children, err := readDirectory(context.Background(), home, f)
		if err != nil || len(children.Items) != 1 {
			t.Fatalf("cache browsing failed: %v", err)
		}
		if err := validateRemoval(context.Background(), home, nil, children.Items[0]); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(findingRow(f), f.Label) {
			t.Fatal("cache label is missing from listing")
		}
	}
	if total != int64(4*len(cacheTargets)) {
		t.Fatal("cache paths were double counted")
	}
	for _, path := range protected {
		full := filepath.Join(home, "Library", path)
		info, err := os.Lstat(full)
		if err != nil {
			t.Fatal(err)
		}
		if validateFinding(home, finding{Path: full, info: info}) == nil {
			t.Fatalf("allowlist leaked into protected directory: %s", path)
		}
	}
	m := model{result: result, query: "VS Code"}
	if len(m.visible()) != 5 {
		t.Fatal("displayed app label is not searchable")
	}
}

func TestAppCacheSymlinks(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "Library/Application Support/Code/Cache")
	writeTestFile(t, filepath.Join(path, "data"), "123")
	result := scanLibrary(context.Background(), home, appInventory{})
	if len(result.Items) != 1 {
		t.Fatal("missing cache")
	}
	original := result.Items[0]
	moved := filepath.Join(home, "outside")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, path); err != nil {
		t.Fatal(err)
	}
	result = scanLibrary(context.Background(), home, appInventory{})
	if len(result.Items) != 0 || len(result.Warnings) == 0 {
		t.Fatal("symlinked cache was followed")
	}
	if validateFinding(home, original) == nil {
		t.Fatal("cache replaced by symlink remained removable")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, path); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(path)
	if err := os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent+"-old", parent); err != nil {
		t.Fatal(err)
	}
	result = scanLibrary(context.Background(), home, appInventory{})
	if len(result.Items) != 0 || len(result.Warnings) == 0 {
		t.Fatal("symlinked cache ancestor was followed")
	}
	if validateFinding(home, original) == nil {
		t.Fatal("cache with symlinked ancestor remained removable")
	}
}
