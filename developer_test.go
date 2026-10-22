package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func plist(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>` + body + `</dict></plist>`
}

func TestEntryVersion(t *testing.T) {
	for name, want := range map[string]string{"17.2 (21C62) arm64e": "17.2", "iPhone15,2 17.0 (21A329)": "17", "iOS Device Logs 16.4.db-shm": "16.4", "Watch2,7 5.2.1 (16U113)": "5.2.1", "symbols": ""} {
		version, ok := entryVersion(name)
		var parts []string
		for _, n := range version {
			parts = append(parts, strconv.Itoa(n))
		}
		if got := strings.Join(parts, "."); ok != (want != "") || got != want {
			t.Fatalf("%q: got %q, want %q", name, got, want)
		}
	}
}

func TestDeveloperCategory(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	xcode := filepath.Join(home, xcodeData)
	for _, path := range []string{
		"iOS DeviceSupport/15.0/Symbols/a", "iOS DeviceSupport/16.4 (20E247)/Symbols/a",
		"iOS DeviceSupport/17.2 (21C62)/Symbols/a", "iOS DeviceSupport/17.2 (21C62) arm64e/Symbols/a", "iOS DeviceSupport/iPhone15,2 17.2 (21C62)/Symbols/a",
		"macOS DeviceSupport/14.2.1 (23C71) arm64e/Symbols/a",
		"iOS Device Logs/iOS Device Logs 16.4.db", "iOS Device Logs/iOS Device Logs 16.4.db-shm", "iOS Device Logs/iOS Device Logs 17.0.db",
	} {
		writeTestFile(t, filepath.Join(xcode, path), "1234")
	}
	writeTestFile(t, filepath.Join(xcode, "Archives/2026-09-14/App 14.09.26, 13.40.xcarchive/Info.plist"), plist(`<key>Name</key><string>App</string>
<key>ApplicationProperties</key><dict><key>CFBundleShortVersionString</key><string>7.6</string><key>CFBundleVersion</key><string>7</string></dict>
<key>CreationDate</key><date>2026-09-14T10:40:39Z</date>
<key>Distributions</key><array><dict><key>destination</key><string>export</string></dict><dict><key>uploadEvent</key><dict><key>state</key><string>success</string></dict></dict></array>`))
	writeTestFile(t, filepath.Join(xcode, "DerivedData/Gone-abc/info.plist"), plist(`<key>WorkspacePath</key><string>`+filepath.Join(home, "Dev/Gone/Gone.xcodeproj")+`</string>`))
	writeTestFile(t, filepath.Join(xcode, "DerivedData/Here-def/info.plist"), plist(`<key>WorkspacePath</key><string>`+filepath.Join(home, "Dev/Here.xcworkspace")+`</string>`))
	writeTestFile(t, filepath.Join(home, "Dev/Here.xcworkspace/contents"), "x")
	writeTestFile(t, filepath.Join(home, "Library/Caches/com.apple.dt.Xcode/data"), "12")
	writeTestFile(t, filepath.Join(home, "Library/Caches/com.example.app/data"), "12")

	result := scanLibrary(ctx, home, appInventory{Incomplete: true})
	if len(result.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
	paths := map[string]finding{}
	for _, f := range result.Items {
		paths[strings.TrimPrefix(f.Path, home+"/")] = f
	}
	older := []string{"iOS DeviceSupport/15.0", "iOS DeviceSupport/16.4 (20E247)", "iOS Device Logs/iOS Device Logs 16.4.db", "iOS Device Logs/iOS Device Logs 16.4.db-shm"}
	newest := []string{"iOS DeviceSupport/17.2 (21C62)", "iOS DeviceSupport/17.2 (21C62) arm64e", "iOS DeviceSupport/iPhone15,2 17.2 (21C62)", "macOS DeviceSupport/14.2.1 (23C71) arm64e", "iOS Device Logs/iOS Device Logs 17.0.db"}
	for _, relative := range append(older, newest...) {
		f, ok := paths[filepath.Join(xcodeData, relative)]
		isOlder := slices.Contains(older, relative)
		if !ok || f.Kind != "Developer" || f.Danger == isOlder || f.rememberable(home) != isOlder {
			t.Fatalf("misclassified %s: %+v", relative, f)
		}
		if err := validateRemoval(ctx, home, nil, f); err != nil {
			t.Fatalf("%s: %v", relative, err)
		}
		if isOlder && !strings.Contains(f.Reason, "newer: ") {
			t.Fatalf("older version lacks its successor: %s", f.Reason)
		}
	}
	archive := paths[filepath.Join(xcodeData, "Archives/2026-09-14/App 14.09.26, 13.40.xcarchive")]
	if archive.Label != "Archive / App 7.6 (7), 2026-09-14, uploaded" || !archive.Danger || archive.rememberable(home) || validateRemoval(ctx, home, nil, archive) != nil {
		t.Fatalf("bad archive finding: %+v", archive)
	}
	if f := paths["Library/Caches/com.apple.dt.Xcode"]; f.Kind != "Developer" || f.Danger {
		t.Fatalf("Xcode cache not under Developer: %+v", f)
	}
	if f := paths["Library/Caches/com.example.app"]; f.Kind != "Cache" {
		t.Fatalf("app cache moved: %+v", f)
	}

	// Remembered older versions resolve; a missing one stays missing rather than failing.
	resolved, err := resolveCache(ctx, home, filepath.Join(xcode, "iOS DeviceSupport/16.4 (20E247)/Symbols/a"))
	if err != nil || resolved.Kind != "Developer" || resolved.removalRoot().Path != filepath.Join(xcode, "iOS DeviceSupport/16.4 (20E247)") {
		t.Fatalf("remembered child did not resolve: %+v %v", resolved, err)
	}
	if _, err := resolveCache(ctx, home, filepath.Join(xcode, "iOS DeviceSupport/16.3 (20D47)")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing version is not reported as missing: %v", err)
	}
	// Once nothing newer remains, the former older version needs a rescan.
	for _, relative := range newest[:3] {
		if err := os.RemoveAll(filepath.Join(xcode, relative)); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateRemoval(ctx, home, nil, paths[filepath.Join(xcodeData, "iOS DeviceSupport/16.4 (20E247)")]); err == nil || !strings.Contains(err.Error(), "rescan") {
		t.Fatalf("newest version stayed removable without danger: %v", err)
	}

	_, children, err := readDirectory(ctx, home, paths[filepath.Join(xcodeData, "DerivedData")])
	if err != nil || len(children.Items) != 2 {
		t.Fatalf("DerivedData browsing failed: %v", err)
	}
	labels := map[string]finding{}
	for _, child := range children.Items {
		labels[child.Label] = child
	}
	if f, ok := labels["~/Dev/Gone/Gone.xcodeproj (missing)"]; !ok || !strings.HasPrefix(f.Reason, "Its project no longer exists") || f.Kind != "Developer" {
		t.Fatalf("missing project not flagged: %v", labels)
	}
	if _, ok := labels["~/Dev/Here.xcworkspace"]; !ok {
		t.Fatalf("project label missing: %v", labels)
	}
}

func TestDeveloperTreesAreRemembered(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writeTestFile(t, filepath.Join(home, ".m2/repository/local-artifact"), "1234")
	writeTestFile(t, filepath.Join(home, "Library/Containers/com.example.editor/Data/keep"), "12")
	m := model{home: home, result: scanLibrary(ctx, home, appInventory{Incomplete: true})}
	for _, f := range m.result.Items {
		tree := f.Path == filepath.Join(home, ".m2")
		if f.Danger != true || f.rememberable(home) != tree {
			t.Fatalf("wrong remembering grade: %+v", f)
		}
		if err := m.rememberCache(f); err != nil || m.remembered[f.Path] != tree {
			t.Fatalf("remembering %s: %v", f.Path, err)
		}
	}
	// A remembered tree keeps its danger grade through resolution and in the CLI listing.
	resolved, err := resolveCache(ctx, home, filepath.Join(home, ".m2/repository"))
	if err != nil || !resolved.Danger || resolved.removalRoot().Path != filepath.Join(home, ".m2") || validateRemoval(ctx, home, nil, resolved) != nil {
		t.Fatalf("remembered tree child did not resolve: %+v %v", resolved, err)
	}
	var out strings.Builder
	if err := cleanRemembered(ctx, home, strings.NewReader("n\n"), &out, false); err != nil || !strings.Contains(out.String(), "[danger]") || !strings.Contains(out.String(), "Cancelled") {
		t.Fatalf("clean listing: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".m2/repository/local-artifact")); err != nil {
		t.Fatal("cancelled clean touched the tree")
	}
}

func TestSimulatorsAndMounts(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	devices := filepath.Join(home, "Library/Developer/CoreSimulator/Devices")
	device := filepath.Join(devices, "FB536CD9-4B11-4F82-A8C2-5AA0F7B5978E")
	writeTestFile(t, filepath.Join(device, "device.plist"), plist(`<key>name</key><string>iPhone 18 Pro</string><key>runtime</key><string>com.apple.CoreSimulator.SimRuntime.iOS-27-0</string><key>state</key><integer>3</integer>`))
	writeTestFile(t, filepath.Join(device, "data/app"), "1234")
	writeTestFile(t, filepath.Join(home, "Library/Developer/Xcode/UserData/settings"), "12")
	result := scanLibrary(ctx, home, appInventory{Incomplete: true})
	var simulator finding
	for _, f := range result.Items {
		if f.Path == filepath.Join(home, xcodeData) {
			t.Fatal("Xcode folder listed whole beside its entries")
		}
		if f.Path == filepath.Join(home, "Library/Developer/CoreSimulator") {
			simulator = f
		}
	}
	if simulator.Kind != "Developer" || !simulator.Danger || validateRemoval(ctx, home, nil, simulator) != nil || storageRoot(home, filepath.Join(home, xcodeData)) {
		t.Fatalf("bad simulator finding: %+v", simulator)
	}
	_, children, err := readDirectory(ctx, home, simulator)
	if err != nil || len(children.Items) != 1 {
		t.Fatalf("browsing CoreSimulator: %v", err)
	}
	_, children, err = readDirectory(ctx, home, children.Items[0])
	if err != nil || len(children.Items) != 1 || children.Items[0].Label != "iPhone 18 Pro, iOS 27.0, booted" || !children.Items[0].Danger || !strings.Contains(children.Items[0].Reason, "simctl delete") {
		t.Fatalf("simulator label: %+v %v", children.Items, err)
	}

	// A mount, here devfs, is neither listed nor walked.
	info, err := os.Lstat("/dev")
	if err != nil {
		t.Fatal(err)
	}
	var mounted scanResult
	if err := mounted.addFinding(ctx, finding{Path: "/dev", info: info}); err != nil || len(mounted.Items) != 0 || len(mounted.Warnings) != 1 {
		t.Fatalf("mount was walked: %+v %v", mounted, err)
	}
}
