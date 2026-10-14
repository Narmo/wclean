package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Exact home-relative locations only. In particular, Service Worker storage, user profiles,
// backups, histories, and installed packages are not ordinary disposable caches.
var cacheTargets = []struct{ path, label string }{
	{"Library/Application Support/Code/Cache", "VS Code / Cache"},
	{"Library/Application Support/Code/Code Cache", "VS Code / Code Cache"},
	{"Library/Application Support/Code/GPUCache", "VS Code / GPUCache"},
	{"Library/Application Support/Code/CachedData", "VS Code / CachedData"},
	{"Library/Application Support/Code/CachedExtensionVSIXs", "VS Code / Extension downloads"},
	{"Library/Application Support/discord/Cache", "Discord / Cache"},
	{"Library/Application Support/discord/Code Cache", "Discord / Code Cache"},
	{"Library/Application Support/discord/GPUCache", "Discord / GPUCache"},
	{"Library/Application Support/Claude/Cache", "Claude / Cache"},
	{"Library/Application Support/Claude/Code Cache", "Claude / Code Cache"},
	{"Library/Application Support/Claude/GPUCache", "Claude / GPUCache"},
	{"Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/Cache", "Slack / Cache"},
	{"Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/Code Cache", "Slack / Code Cache"},
	{"Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack/GPUCache", "Slack / GPUCache"},
	{"Library/Arduino15/staging", "Arduino / Download staging"},
	{"Library/Developer/Xcode/DerivedData", "Xcode / DerivedData"},
	{"Library/Developer/Xcode/DocumentationCache", "Xcode / Documentation downloads"},
	{".android/cache", "Android / SDK repository cache"},
	{".cache/gh", "GitHub CLI / HTTP cache"},
	{".cache/fontconfig", "Fontconfig / Font cache"},
	{".cache/mesa_shader_cache", "Mesa / Shader cache"},
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
	if err != nil || real != path || !info.IsDir() {
		result.Warnings = append(result.Warnings, "Skipped inaccessible, symlinked, or non-directory cache: "+path)
		return nil
	}
	return result.addFinding(ctx, finding{Path: path, Kind: "Cache", Label: label, Reason: reason, info: info})
}
