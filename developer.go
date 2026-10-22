package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const xcodeData = "Library/Developer/Xcode"

// Xcode keeps one entry per OS version in these folders; only the newest is
// still needed for the devices you debug today.
var versionedFolders = []string{"iOS DeviceSupport", "watchOS DeviceSupport", "tvOS DeviceSupport", "visionOS DeviceSupport", "macOS DeviceSupport", "iOS Device Logs"}

// Developer tool entries in ~/Library/Caches, besides every com.apple.dt.* one.
var developerCacheNames = []string{"claude-cli-nodejs", "CocoaPods", "com.DanPristupov.Fork", "com.microsoft.VSCode", "com.microsoft.VSCode.ShipIt", "com.openai.codex", "Docker Desktop", "go-build", "Homebrew", "JetBrains", "main.kts.compiled.cache", "ms-playwright", "org.swift.swiftpm", "org.webkit.Playwright", "pip", "pip-audit", "swift-frontend", "typescript", "Yarn"}

// Developer data gets its own category but keeps the safeguards of its grade:
// danger entries still need a danger confirmation and are never remembered.
func developerPath(home, path string) bool {
	if withinPath(filepath.Join(home, "Library/Developer"), path) {
		return true
	}
	relative, err := filepath.Rel(filepath.Join(home, "Library/Caches"), path)
	if name := strings.Split(relative, string(filepath.Separator))[0]; err == nil && (slices.Contains(developerCacheNames, name) || strings.HasPrefix(name, "com.apple.dt.")) {
		return true
	}
	for _, target := range slices.Concat(developerCaches, developerTrees) {
		if withinPath(filepath.Join(home, target.path), path) {
			return true
		}
	}
	return false
}

// developerTree returns the danger-grade tree containing path; unlike other
// danger entries, these are remembered for `wclean clean`.
func developerTree(home, path string) (root, label string) {
	for _, tree := range developerTrees {
		if root := filepath.Join(home, tree.path); withinPath(root, path) {
			return root, tree.label
		}
	}
	return "", ""
}

func category(home, path, kind string) string {
	if developerPath(home, path) {
		return "Developer"
	}
	return kind
}

var osVersion = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)(?:\.[A-Za-z][A-Za-z-]*)?$`)

// Names look like "17.2 (21C62) arm64e", "iPhone15,2 17.2 (21C62)", or
// "iOS Device Logs 17.2.db"; the first numeric field is the OS version.
func entryVersion(name string) ([]int, bool) {
	for _, field := range strings.Fields(name) {
		match := osVersion.FindStringSubmatch(field)
		if match == nil {
			continue
		}
		var version []int
		for _, part := range strings.Split(match[1], ".") {
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, false
			}
			version = append(version, n)
		}
		// 17.0 and 17 name the same release.
		for len(version) > 0 && version[len(version)-1] == 0 {
			version = version[:len(version)-1]
		}
		return version, true
	}
	return nil, false
}

// versionedRoot returns the version entry containing path in a versioned Xcode folder.
func versionedRoot(home, path string) string {
	relative, err := filepath.Rel(filepath.Join(home, xcodeData), path)
	parts := strings.Split(relative, string(filepath.Separator))
	if err != nil || len(parts) < 2 || !slices.Contains(versionedFolders, parts[0]) || strings.HasPrefix(parts[1], ".") {
		return ""
	}
	if _, ok := entryVersion(parts[1]); !ok {
		return ""
	}
	return filepath.Join(home, xcodeData, parts[0], parts[1])
}

// newerVersion names the newest sibling when path is an older version entry.
// Equal versions, such as one release in two architectures, are never older.
func newerVersion(home, path string) string {
	if versionedRoot(home, path) != path {
		return ""
	}
	version, _ := entryVersion(filepath.Base(path))
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return ""
	}
	logs := filepath.Base(filepath.Dir(path)) == "iOS Device Logs"
	newest, newer := version, ""
	for _, entry := range entries {
		v, ok := entryVersion(entry.Name())
		shape := logs && entry.Type().IsRegular() || !logs && entry.IsDir()
		if ok && shape && !strings.HasPrefix(entry.Name(), ".") && slices.Compare(v, newest) > 0 {
			newest, newer = v, entry.Name()
		}
	}
	return newer
}

// Device log databases are files; every other cache target is a directory.
func cacheShapeOK(home, path string, info os.FileInfo) bool {
	if filepath.Dir(path) == filepath.Join(home, xcodeData, "iOS Device Logs") {
		return info.Mode().IsRegular()
	}
	return info.IsDir()
}

// Archives and versioned folders are listed per entry, archives inside their
// date folders, so the parent folders themselves are never findings.
func expandedRoot(home, path string) bool {
	relative, err := filepath.Rel(filepath.Join(home, xcodeData), path)
	parts := strings.Split(relative, string(filepath.Separator))
	if err != nil || slices.ContainsFunc(parts, func(part string) bool { return strings.HasPrefix(part, ".") }) {
		return false
	}
	return len(parts) == 2 && (parts[0] == "Archives" || slices.Contains(versionedFolders, parts[0])) ||
		len(parts) == 3 && parts[0] == "Archives" && !strings.HasSuffix(parts[1], ".xcarchive")
}

func expandedFolder(home, path string) bool {
	return filepath.Dir(path) == filepath.Join(home, xcodeData) && (filepath.Base(path) == "Archives" || slices.Contains(versionedFolders, filepath.Base(path)))
}

// Older versions become remembered caches; the newest versions and every
// archive keep a danger confirmation.
func (result *scanResult) scanXcodeFolder(ctx context.Context, home, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", dir, err))
		return nil
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		archives := filepath.Base(dir) == "Archives"
		if archives && entry.IsDir() && !strings.HasSuffix(entry.Name(), ".xcarchive") {
			err = result.scanXcodeFolder(ctx, home, path) // A date folder.
		} else if label, _ := cacheDetails(home, path); label != "" {
			err = result.addCache(ctx, home, path)
		} else {
			relative, _ := filepath.Rel(filepath.Join(home, xcodeData), path)
			label, reason := strings.ReplaceAll(relative, string(filepath.Separator), " / "), storageReason
			if strings.HasSuffix(entry.Name(), ".xcarchive") {
				label, reason = archiveDetails(ctx, path, label)
			} else if _, ok := entryVersion(entry.Name()); ok && filepath.Base(dir) == "iOS Device Logs" {
				reason = "Newest device log database; Xcode keeps current device logs in it."
			} else if ok && slices.Contains(versionedFolders, filepath.Base(dir)) {
				reason = "Newest version here; Xcode uses these symbols for devices on this OS and copies them again on the next connection, which takes a while."
			}
			err = result.addStorage(ctx, home, path, label, reason)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// kind is a plutil -expect type, or json for a whole dictionary or array.
func plistValue(ctx context.Context, plist, key, kind string) string {
	args := []string{"-extract", key, "raw", "-expect", kind}
	if kind == "json" {
		args = []string{"-extract", key, "json"}
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/plutil", append(args, "-o", "-", plist)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Labels come from Xcode's Info.plist; unreadable fields are left out, and no
// recorded upload is not proof that the build never shipped.
func archiveDetails(ctx context.Context, path, fallback string) (label, reason string) {
	reason = "Danger: an Xcode archive holds the distributed build and the debug symbols needed to symbolicate its crash reports. It cannot be regenerated."
	plist := filepath.Join(path, "Info.plist")
	name := plistValue(ctx, plist, "Name", "string")
	if name == "" {
		return fallback, reason
	}
	var app struct {
		Version string `json:"CFBundleShortVersionString"`
		Build   string `json:"CFBundleVersion"`
	}
	type distribution struct {
		UploadEvent struct{ State string } `json:"uploadEvent"`
	}
	var distributions []distribution
	// Unreadable or non-JSON values simply leave the fields empty.
	_ = json.Unmarshal([]byte(plistValue(ctx, plist, "ApplicationProperties", "json")), &app)
	_ = json.Unmarshal([]byte(plistValue(ctx, plist, "Distributions", "json")), &distributions)
	label = "Archive / " + name
	if app.Version != "" {
		label += " " + app.Version
	}
	if app.Build != "" {
		label += " (" + app.Build + ")"
	}
	if date := filepath.Base(filepath.Dir(path)); date != "Archives" {
		label += ", " + date
	}
	if slices.ContainsFunc(distributions, func(d distribution) bool { return d.UploadEvent.State == "success" }) {
		label += ", uploaded"
		reason += " It records a successful upload to Apple."
	}
	return label, reason
}

// Device sets of Xcode, test clones, and playgrounds; each device folder has a device.plist.
var simulatorSets = []string{"Library/Developer/CoreSimulator/Devices", "Library/Developer/XCTestDevices", "Library/Developer/XCPGDevices"}

func simulatorDetails(ctx context.Context, path, reason string) (string, string) {
	plist := filepath.Join(path, "device.plist")
	label := plistValue(ctx, plist, "name", "string")
	if label == "" {
		return "", reason
	}
	if runtime, ok := strings.CutPrefix(plistValue(ctx, plist, "runtime", "string"), "com.apple.CoreSimulator.SimRuntime."); ok {
		platform, version, _ := strings.Cut(runtime, "-")
		label += ", " + platform + " " + strings.ReplaceAll(version, "-", ".")
	}
	if plistValue(ctx, plist, "state", "integer") == "3" {
		label += ", booted"
	}
	return label, "Danger: a simulator's apps, their data, and settings. Shut it down and quit Simulator first; `xcrun simctl delete` is the supported, permanent alternative."
}

// DerivedData folders record the workspace they were built from.
func derivedDataDetails(ctx context.Context, home, path, reason string) (string, string) {
	workspace := plistValue(ctx, filepath.Join(path, "info.plist"), "WorkspacePath", "string")
	if !filepath.IsAbs(workspace) {
		return "", reason
	}
	label := workspace
	if strings.HasPrefix(workspace, home+string(filepath.Separator)) {
		label = "~" + strings.TrimPrefix(workspace, home)
	}
	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		return label + " (missing)", "Its project no longer exists at that path. " + reason
	}
	return label, reason
}
