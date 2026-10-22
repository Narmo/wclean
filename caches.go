package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Exact home-relative locations only. In particular, Service Worker storage, user profiles,
// backups, histories, and installed packages are not ordinary disposable caches.
var cacheTargets = slices.Concat([]struct{ path, label string }{
	{"Library/Application Support/discord/Cache", "Discord / Cache"},
	{"Library/Application Support/discord/Code Cache", "Discord / Code Cache"},
	{"Library/Application Support/discord/GPUCache", "Discord / GPUCache"},
	{"Library/Application Support/Claude/Cache", "Claude / Cache"},
	{"Library/Application Support/Claude/Code Cache", "Claude / Code Cache"},
	{"Library/Application Support/Claude/GPUCache", "Claude / GPUCache"},
	{"Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/Cache", "Slack / Cache"},
	{"Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/Code Cache", "Slack / Code Cache"},
	{"Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/GPUCache", "Slack / GPUCache"},
	{".cache/fontconfig", "Fontconfig / Font cache"},
	{".cache/mesa_shader_cache", "Mesa / Shader cache"},
}, developerCaches)

// Developer tool caches are listed under Developer instead of Caches.
var developerCaches = []struct{ path, label string }{
	{"Library/Application Support/Code/Cache", "VS Code / Cache"},
	{"Library/Application Support/Code/Code Cache", "VS Code / Code Cache"},
	{"Library/Application Support/Code/GPUCache", "VS Code / GPUCache"},
	{"Library/Application Support/Code/CachedData", "VS Code / CachedData"},
	{"Library/Application Support/Code/CachedExtensionVSIXs", "VS Code / Extension downloads"},
	{"Library/Arduino15/staging", "Arduino / Download staging"},
	{"Library/Developer/Xcode/DerivedData", "Xcode / DerivedData"},
	{"Library/Developer/Xcode/DocumentationCache", "Xcode / Documentation downloads"},
	{"Library/Developer/Shared/Documentation", "Xcode / Old documentation downloads"},
	{".android/cache", "Android / SDK repository cache"},
	{".cache/gh", "GitHub CLI / HTTP cache"},
	{".claude/cache", "Claude Code / Metadata cache"},
	{".codex/cache", "Codex / Metadata and asset cache"},
	{".npm/_cacache", "npm / Package download cache"},
	{".local/pipx/.cache", "pipx / Temporary run environments"},
}

func cacheDetails(home, path string) (label, reason string) {
	for _, target := range cacheTargets {
		if path == filepath.Join(home, target.path) {
			reason = "Explicit application cache; quit the app and stop downloads first. Data may be regenerated or downloaded again."
			if target.path == "Library/Developer/Xcode/DerivedData" {
				reason = "Generated Xcode build data and indexes; quit Xcode and stop builds first. Rebuilding and indexing will take time."
			}
			if target.path == "Library/Developer/Xcode/DocumentationCache" {
				reason = "Downloaded Xcode documentation; quit Xcode first. Documentation may need downloading again."
			}
			if target.path == "Library/Developer/Shared/Documentation" {
				reason = "Offline documentation sets downloaded by old Xcode releases; current Xcode neither downloads nor reads them. Other documentation viewers may still use them."
			}
			if target.path == ".local/pipx/.cache" {
				reason = "Temporary pipx run environments, not installed pipx tools; stop all pipx-run commands first. Environments will need downloading and creating again."
			}
			return target.label, reason
		}
	}
	relative, err := filepath.Rel(filepath.Join(home, "Library", "Containers"), path)
	parts := strings.Split(relative, string(filepath.Separator))
	if err == nil && len(parts) == 4 && bundleID.MatchString(parts[0]) && !strings.HasPrefix(strings.ToLower(parts[0]), "com.apple.") && filepath.Join(parts[1:]...) == "Data/Library/Caches" {
		return parts[0] + " / Container caches", "Standard third-party container cache; quit the owning app first. It may be regenerated or contain offline downloads. Other container data requires a danger confirmation."
	}
	if newer := newerVersion(home, path); newer != "" {
		folder := filepath.Base(filepath.Dir(path))
		reason = "Debug symbols Xcode copied from a device on an older OS; newer: " + newer + ". Quit Xcode first. Connecting a device on this version makes Xcode copy them again, which takes a while."
		if folder == "iOS Device Logs" {
			reason = "Device log database for an older iOS version; newer: " + newer + ". Newer Xcode releases appear to migrate older databases, so this one likely duplicates newer logs. Quit Xcode first."
		}
		return folder + " / " + filepath.Base(path), reason
	}
	return "", ""
}

func (result *scanResult) scanAppCaches(ctx context.Context, home string) {
	for _, target := range cacheTargets {
		if err := result.addCache(ctx, home, filepath.Join(home, target.path)); err != nil {
			return
		}
	}
}

func (result *scanResult) addCache(ctx context.Context, home, path string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	label, reason := cacheDetails(home, path)
	if label == "" {
		return nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", path, err))
		return nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path || !cacheShapeOK(home, path, info) {
		result.Warnings = append(result.Warnings, "Skipped inaccessible, symlinked, or non-directory cache: "+path)
		return nil
	}
	return result.addFinding(ctx, finding{Path: path, Kind: category(home, path, "Cache"), Label: label, Reason: reason, info: info})
}
