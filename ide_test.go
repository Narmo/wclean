package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstalledIDEProtection(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS plutil")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{filepath.Join(home, "Applications")}
	for _, folder := range []string{
		"JetBrains/IntelliJIdea2025.1", "JetBrains/IntelliJIdea2026.1",
		"Google/AndroidStudio2025.1", "Google/AndroidStudio2026.1",
		"JetBrains/WebStorm2024.1", "JetBrains/WebStorm2026.1",
	} {
		writeTestFile(t, filepath.Join(home, "Library/Application Support", folder, "settings"), "keep")
	}
	writeTestFile(t, filepath.Join(home, "Library/Caches/example/data"), "cache")
	addIDE := func(app, id, name, code string) string {
		path := filepath.Join(roots[0], app+".app/Contents")
		writeTestFile(t, filepath.Join(path, "Info.plist"), `<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>`+id+`</string></dict></plist>`)
		metadata := filepath.Join(path, "Resources/product-info.json")
		writeTestFile(t, metadata, `{"dataDirectoryName":"`+name+`","productCode":"`+code+`"}`)
		return metadata
	}
	addIDE("IDEA old", "com.jetbrains.intellij", "IntelliJIdea2025.1", "IU")
	addIDE("IDEA new", "com.jetbrains.intellij", "IntelliJIdea2026.1", "IU")
	addIDE("Android Studio old", "com.google.android.studio", "AndroidStudio2025.1", "AI")
	result := scan(context.Background(), home, roots)
	if len(result.Warnings) != 0 || len(result.Items) != 2 {
		t.Fatalf("installed IDE folders weren't protected: %+v", result)
	}
	var candidate finding
	for _, f := range result.Items {
		if f.Kind == "Leftover" {
			if filepath.Base(f.Path) != "WebStorm2024.1" {
				t.Fatalf("protected folder listed: %+v", f)
			}
			candidate = f
		}
	}
	if candidate.Path == "" {
		t.Fatal("unreferenced older folder missing")
	}
	if err := validateRemoval(context.Background(), home, roots, candidate); err != nil {
		t.Fatal(err)
	}
	_, children, err := readDirectory(context.Background(), home, candidate)
	if err != nil || len(children.Items) != 1 {
		t.Fatalf("IDE child listing: %v, %+v", err, children)
	}
	child := children.Items[0]
	if err := validateRemoval(context.Background(), home, roots, child); err != nil {
		t.Fatal(err)
	}
	metadata := addIDE("WebStorm old", "com.jetbrains.webstorm", "WebStorm2024.1", "WS")
	if validateRemoval(context.Background(), home, roots, child) == nil {
		t.Fatal("IDE child bypassed installed-owner protection")
	}
	if validateRemoval(context.Background(), home, roots, candidate) == nil {
		t.Fatal("new IDE installation did not block removal")
	}
	writeTestFile(t, metadata, "broken JSON")
	if validateRemoval(context.Background(), home, roots, child) == nil {
		t.Fatal("IDE child bypassed incomplete-inventory protection")
	}
	result = scan(context.Background(), home, roots)
	if len(result.Warnings) == 0 || len(result.Items) != 1 || result.Items[0].Kind != "Cache" {
		t.Fatalf("incomplete IDE metadata must suppress IDE candidates: %+v", result)
	}
	if validateRemoval(context.Background(), home, roots, candidate) == nil {
		t.Fatal("incomplete IDE metadata allowed removal")
	}
}

func TestIDEMetadataFailures(t *testing.T) {
	for _, data := range []string{
		`{}`, `{"dataDirectoryName":"../outside"}`, `{"dataDirectoryName":""}`,
		`{"dataDirectoryName":"IntelliJIdea2026.1","launch":[{"additionalJvmArguments":["-Didea.config.path=/custom"]}]}`,
		`{"dataDirectoryName":"IntelliJIdea2026.1","launch":[{"additionalJvmArguments":["-Didea.paths.selector=IntelliJIdea2025.1"]}]}`,
	} {
		app := filepath.Join(t.TempDir(), "IDE.app")
		writeTestFile(t, filepath.Join(app, "Contents/Resources/product-info.json"), data)
		result := appInventory{IDEDirs: map[string]bool{}}
		result.readIDE(app, "com.jetbrains.intellij")
		if !result.IDEIncomplete || len(result.IDEDirs) != 0 {
			t.Fatalf("unsafe metadata accepted: %s", data)
		}
	}
	result := appInventory{IDEDirs: map[string]bool{}}
	result.readIDE(t.TempDir(), "com.jetbrains.intellij")
	if !result.IDEIncomplete {
		t.Fatal("missing metadata for known IDE accepted")
	}
}

func TestOlderIDEFolders(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folders := []string{
		"JetBrains/IntelliJIdea2025.9", "JetBrains/IntelliJIdea2025.10",
		"JetBrains/IdeaIC2024.1", "JetBrains/PyCharm2025.1", "JetBrains/PyCharm2025.1.0",
		"JetBrains/WebStorm2024.1", "JetBrains/WebStorm2026.1",
		"Google/AndroidStudio2025.2.9", "Google/AndroidStudio2025.2.10",
		"Google/AndroidStudioPreview2024.1", "Google/GoogleUpdater2024.1",
		"JetBrains/IntelliJIdea2023.1-backup", "JetBrains/consentOptions",
	}
	for _, folder := range folders {
		path := filepath.Join(home, "Library/Application Support", folder)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "settings"), []byte("12345"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := filepath.Join(home, "Library/Application Support/JetBrains")
	if err := os.Symlink(filepath.Join(base, "IntelliJIdea2025.10"), filepath.Join(base, "IntelliJIdea2026.1")); err != nil {
		t.Fatal(err)
	}
	result := scanLibrary(context.Background(), home, appInventory{})
	if len(result.Warnings) != 0 || len(result.Items) != 3 {
		t.Fatalf("unexpected scan: %+v", result)
	}
	want := map[string]bool{"IntelliJIdea2025.9": true, "WebStorm2024.1": true, "AndroidStudio2025.2.9": true}
	for _, f := range result.Items {
		if !want[filepath.Base(f.Path)] || f.Size != 5 || f.Kind != "Leftover" {
			t.Fatalf("unexpected candidate: %+v", f)
		}
		if err := validateFinding(home, f); err != nil {
			t.Fatal(err)
		}
		if filepath.Base(f.Path) == "IntelliJIdea2025.9" {
			latest := filepath.Join(base, "IntelliJIdea2025.10")
			if err := os.Rename(latest, latest+"-moved"); err != nil {
				t.Fatal(err)
			}
			if validateFinding(home, f) == nil {
				t.Fatal("removal allowed after newer version disappeared")
			}
		}
	}
	path := filepath.Join(base, "consentOptions")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if validateFinding(home, finding{Path: path, info: info}) == nil {
		t.Fatal("unrecognized nested folder allowed")
	}
}
