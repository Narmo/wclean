package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryPlutil(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS plutil")
	}
	for _, format := range []string{"xml1", "binary1"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			layouts := map[string]string{
				"Example.app/Contents/Info.plist":                             "com.example.editor",
				"Example.app/Contents/Helpers/Helper.app/Contents/Info.plist": "com.example.helper",
				"Runner.app/Info.plist":                                       "com.example.runner",
				"Wrapper.app/Wrapper/Mobile.app/Info.plist":                   "com.example.mobile",
			}
			for relative, id := range layouts {
				path := filepath.Join(root, relative)
				writeTestFile(t, path, `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>`+id+`</string></dict></plist>`)
				if out, err := exec.Command("/usr/bin/plutil", "-convert", format, path).CombinedOutput(); err != nil {
					t.Fatalf("convert: %v: %s", err, out)
				}
			}
			for link, target := range map[string]string{
				"Wrapper.app/WrappedBundle":      "Wrapper/Mobile.app",
				"Alias.app":                      "Example.app",
				"Example.app/Contents/Cycle.app": "../..",
			} {
				if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
					t.Fatal(err)
				}
			}
			rootAlias := filepath.Join(t.TempDir(), "Applications")
			if err := os.Symlink(root, rootAlias); err != nil {
				t.Fatal(err)
			}
			result := inventory(context.Background(), []string{rootAlias, root})
			if result.Incomplete || len(result.Warnings) != 0 || len(result.IDs) != len(layouts) {
				t.Fatalf("unexpected inventory: %+v", result)
			}
			for _, id := range layouts {
				if !result.IDs[id] {
					t.Errorf("missing ID %s", id)
				}
			}
			if result.IDs["example"] {
				t.Fatal("display name stored as an ID")
			}

			// A directory with an .app suffix but no bundle metadata is informational.
			if err := os.Mkdir(filepath.Join(root, "Daemon.app"), 0700); err != nil {
				t.Fatal(err)
			}
			result = inventory(context.Background(), []string{root, rootAlias})
			if result.Incomplete || len(result.Warnings) != 1 {
				t.Fatalf("non-bundle treated as inventory failure or duplicated: %+v", result)
			}

			// A plist that exists but cannot supply a valid ID must still block detection.
			writeTestFile(t, filepath.Join(root, "Broken.app/Contents/Info.plist"), `<plist version="1.0"><dict><key>CFBundleIdentifier</key><integer>42</integer></dict></plist>`)
			result = inventory(context.Background(), []string{root})
			if !result.Incomplete || len(result.Warnings) != 2 || result.IDs["42"] {
				t.Fatalf("invalid ID accepted: %+v", result)
			}
		})
	}
}

func TestInventoryFailures(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS plutil")
	}
	root := t.TempDir()
	if result := inventory(context.Background(), []string{filepath.Join(root, "missing")}); !result.Incomplete {
		t.Fatal("missing search root accepted as complete")
	}
	path := filepath.Join(root, "Bad.app/Contents/Info.plist")
	writeTestFile(t, path, "not a plist")
	if result := inventory(context.Background(), []string{root}); !result.Incomplete {
		t.Fatal("malformed plist accepted")
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(path, 0600) })
		if result := inventory(context.Background(), []string{root}); !result.Incomplete {
			t.Fatal("unreadable plist accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := inventory(ctx, []string{root}); !result.Incomplete {
		t.Fatal("cancelled inventory accepted")
	}
}
