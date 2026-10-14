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
		writeTestFile(t, filepath.Join(home, target.path, "data"), "1234")
	}
	protected := []string{
		"Library/Application Support/Code/User/globalStorage",
		"Library/Application Support/Code/Backups",
		"Library/Application Support/Code/Service Worker/CacheStorage",
		"Library/Application Support/discord/Local Storage",
		"Library/Application Support/Claude/local-agent-mode-sessions",
		"Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/Service Worker/CacheStorage",
		"Library/Containers/com.apple.CoreDevice.CoreDeviceService/Data/Library/Caches/AppInstallationBinaryDeltas",
		"Library/Arduino15/packages",
		"Library/Caches/.hidden",
		".android", ".android/adbkey", ".android/debug.keystore", ".android/avd", ".android/cli",
		".cache", ".cache/unknown", ".cache/codex-runtimes", ".cache/claude/staging", ".cache/firebase",
		".claude", ".claude/.credentials.json", ".claude/settings.json", ".claude/projects",
		".claude/sessions", ".claude/backups", ".claude/plugins/cache", ".claude/paste-cache", ".claude/cache-backup",
		".codex", ".codex/auth.json", ".codex/config.toml", ".codex/sessions", ".codex/archived_sessions",
		".codex/sqlite", ".codex/generated_images", ".codex/shell_snapshots", ".codex/plugins/cache", ".codex/packages",
		".npm", ".npm/_npx", ".npm/_cacache-backup", ".npmrc",
		".m2", ".m2/repository/local-artifact", ".m2/settings.xml",
		".local", ".local/bin", ".local/share", ".local/state", ".local/pipx/venvs", ".local/pipx/shared",
		".local/pipx/.cache-backup",
		".pi", ".pi/agent/auth.json", ".pi/agent/settings.json", ".pi/agent/sessions",
		".pi/agent/git", ".pi/agent/npm", ".pi/agent/install",
		".vscode", ".vscode/extensions", ".vscode/cli", ".vscode/agent-plugins", ".vscode/argv.json",
	}
	for _, path := range protected {
		writeTestFile(t, filepath.Join(home, path, "keep"), "protected")
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
		for _, item := range []finding{f, children.Items[0]} {
			resolved, err := resolveCache(context.Background(), home, item.Path)
			if err != nil || resolved.Size != 4 || resolved.Kind != "Cache" {
				t.Fatalf("remembered cache resolution failed for %s: %v", item.Path, err)
			}
		}
		if !strings.Contains(findingRow(f), f.Label) {
			t.Fatal("cache label is missing from listing")
		}
	}
	if total != int64(4*len(cacheTargets)) {
		t.Fatal("cache paths were double counted")
	}
	for _, path := range protected {
		full := filepath.Join(home, path)
		info, err := os.Lstat(full)
		if err != nil {
			t.Fatal(err)
		}
		if validateFinding(home, finding{Path: full, Kind: "Cache", info: info}) == nil {
			t.Fatalf("allowlist leaked into protected directory: %s", path)
		}
		if _, err := cacheRootFor(home, full); err == nil {
			t.Fatalf("protected path can be remembered as a cache: %s", path)
		}
		if _, err := cacheRootFor(home, filepath.Join(full, "keep")); err == nil {
			t.Fatalf("protected child can be remembered as a cache: %s", path)
		}
	}
	m := model{result: result, query: "VS Code"}
	if len(m.visible()) != 5 {
		t.Fatal("displayed app label is not searchable")
	}
}

func TestAppCacheSymlinks(t *testing.T) {
	for _, relative := range []string{
		"Library/Application Support/Code/Cache",
		".android/cache", ".cache/gh", ".claude/cache", ".codex/cache",
		".npm/_cacache", ".local/pipx/.cache",
	} {
		t.Run(relative, func(t *testing.T) {
			testAppCacheSymlinks(t, relative)
		})
	}
}

func testAppCacheSymlinks(t *testing.T, relative string) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, relative)
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
	if _, err := resolveCache(context.Background(), home, path); err == nil {
		t.Fatal("remembered cache followed a symlink")
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
	if _, err := resolveCache(context.Background(), home, path); err == nil {
		t.Fatal("remembered cache followed a symlinked ancestor")
	}
}
