package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type finding struct {
	Path, Kind, Reason, Label string
	Size                      int64
	Incomplete, Missing       bool
	info                      os.FileInfo
	parent                    *finding // Browsed children remain anchored to their detected root.
}
type scanResult struct {
	Items    []finding
	Warnings []string
}

var libraryFolders = []string{"Caches", "Preferences", "Application Support", "Saved Application State", "Application Support/JetBrains", "Application Support/Google"}
// Two-component IDs are accepted only behind a TLD-like first label, which
// keeps org.example apart from krita.log or default.store.
var bundleID = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*(\.[A-Za-z0-9_-]+){2,}|[A-Za-z]{2,4}\.[A-Za-z0-9_-]+)$`)

func libraryID(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(name, ".plist"), ".savedState"))
}

// Apple domains and shared app-group containers belong to the system or to a
// family of apps, so no single missing app makes them removable.
func systemDomain(id string) bool {
	if strings.HasPrefix(id, "com.apple.") || strings.Contains(id, ".com.apple.") {
		return true
	}
	for _, part := range strings.Split(id, ".") {
		if part == "group" || part == "systemgroup" {
			return true
		}
	}
	return false
}

// Helpers, plugins, and XPC services write under their app's own ID namespace.
func ownedByInstalled(installed appInventory, id string) bool {
	for cut := strings.LastIndex(id, "."); cut > 0; cut = strings.LastIndex(id[:cut], ".") {
		if installed.IDs[id[:cut]] {
			return true
		}
	}
	return false
}

func scan(ctx context.Context, home string, roots []string) scanResult {
	installed := inventory(ctx, roots)
	result := scanLibrary(ctx, home, installed)
	result.Warnings = append(installed.Warnings, result.Warnings...)
	if installed.Incomplete {
		result.Warnings = append(result.Warnings, "Leftover detection disabled because the application inventory is incomplete. Caches remain available.")
	} else if installed.IDEIncomplete {
		result.Warnings = append(result.Warnings, "Older IDE detection disabled because IDE metadata is incomplete. Other findings remain available.")
	}
	return result
}

func scanLibrary(ctx context.Context, home string, installed appInventory) scanResult {
	var result scanResult
	var tokens map[string]string
	if !installed.Incomplete {
		tokens = leftoverTokens(home, installed)
	}
	for _, folder := range libraryFolders {
		root := filepath.Join(home, "Library", folder)
		// Do not traverse a redirected Library or category directory.
		real, err := filepath.EvalSymlinks(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || real != root {
			result.Warnings = append(result.Warnings, "Skipped inaccessible or symlinked folder: "+root)
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", root, err))
			continue
		}
		var older map[string]string
		if ideFolder(folder) && !installed.Incomplete && !installed.IDEIncomplete {
			older = olderIDEFolders(entries, folder)
			for name := range older {
				if installed.IDEDirs[strings.ToLower(filepath.Join(folder, name))] {
					delete(older, name)
				}
			}
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return result
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := entry.Name()
			if folder == "Caches" && name == "kitty" {
				continue
			}
			id := libraryID(name)
			kind, reason := "Cache", "Cache data; close the owning app first. It may be recreated or contain offline content."
			if ideFolder(folder) {
				newer, ok := older[name]
				if !ok {
					continue
				}
				kind, reason = "Leftover", "Older IDE data; newer folder: "+newer+". No scanned IDE metadata references this folder. Custom paths or unscanned IDEs may still use it; includes settings, plugins, and scratches."
			} else if folder != "Caches" {
				if installed.Incomplete || systemDomain(id) || installed.IDs[id] || ownedByInstalled(installed, id) {
					continue
				}
				owner, vouched := tokens[id]
				switch {
				case bundleID.MatchString(id):
					kind, reason = "Leftover", "No matching bundle ID in the scanned app locations. This is a candidate, not proof: helpers, shared data, or apps elsewhere may own it."
				case vouched:
					kind, reason = "Leftover", "Named after "+owner+", a bundle ID with no installed app. This is a candidate, not proof: shared data or an app elsewhere may own it."
				default:
					continue
				}
			}
			path := filepath.Join(root, name)
			info, err := os.Lstat(path)
			if err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				continue
			}
			if err := result.addFinding(ctx, finding{Path: path, Kind: kind, Reason: reason, info: info}); err != nil {
				return result
			}
		}
	}
	result.keepCompanionLeftovers(home)
	result.scanAppCaches(ctx, home)
	result.scanStorage(ctx, home)
	result.sort()
	return result
}

// Any framework, installer, or JVM can write a preference domain, so most stale
// plists never belonged to an app of their own. Report one only beside the
// Application Support data it accompanies, where the app's absence is visible.
func (result *scanResult) keepCompanionLeftovers(home string) {
	support := filepath.Join(home, "Library", "Application Support")
	keys := map[string]bool{}
	for _, f := range result.Items {
		if f.Kind == "Leftover" && filepath.Dir(f.Path) == support {
			keys[libraryID(filepath.Base(f.Path))] = true
		}
	}
	companion := func(id string) bool {
		if keys[id] || keys[id[strings.LastIndex(id, ".")+1:]] {
			return true
		}
		for key := range keys {
			if strings.HasPrefix(id, key+".") {
				return true
			}
		}
		return false
	}
	result.Items = slices.DeleteFunc(result.Items, func(f finding) bool {
		dir := filepath.Dir(f.Path)
		if f.Kind != "Leftover" || dir != filepath.Join(home, "Library", "Preferences") && dir != filepath.Join(home, "Library", "Saved Application State") {
			return false
		}
		return !companion(libraryID(filepath.Base(f.Path)))
	})
}

// A human-named folder carries no ownership evidence of its own, so pair it
// with a reverse-DNS sibling whose app is gone: the trailing component of such
// an ID is the app's own name. Tokens that are short, deeply nested, or shared
// with an installed app are too weak to act on.
func leftoverTokens(home string, installed appInventory) map[string]string {
	installedParts := map[string]bool{}
	for id := range installed.IDs {
		for _, part := range strings.Split(id, ".") {
			installedParts[part] = true
		}
	}
	tokens := map[string]string{}
	for _, folder := range libraryFolders {
		if folder == "Caches" || ideFolder(folder) {
			continue
		}
		// A folder that cannot be read yields no evidence, and the scan warns about it.
		entries, _ := os.ReadDir(filepath.Join(home, "Library", folder))
		for _, entry := range entries {
			id := libraryID(entry.Name())
			parts := strings.Split(id, ".")
			if len(parts) > 3 || !bundleID.MatchString(id) || systemDomain(id) || installed.IDs[id] || ownedByInstalled(installed, id) {
				continue
			}
			// ponytail: exact name match only; suffixed siblings like <name>rc need a boundary rule that does not also catch unrelated folders.
			if token := parts[len(parts)-1]; len(token) >= 4 && !installedParts[token] {
				tokens[token] = id
			}
		}
	}
	return tokens
}

func (result *scanResult) addFinding(ctx context.Context, f finding) error {
	err := filepath.WalkDir(f.Path, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr == nil && d.Type().IsRegular() {
			var info os.FileInfo
			info, walkErr = d.Info()
			if walkErr == nil {
				f.Size += info.Size()
			}
		}
		if walkErr != nil {
			f.Incomplete = true
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", path, walkErr))
		}
		// WalkDir never follows symlinks; count regular-file logical bytes only.
		return nil
	})
	if err == nil {
		result.Items = append(result.Items, f)
	}
	return err
}

func (result *scanResult) sort() {
	sort.Slice(result.Items, func(i, j int) bool {
		if result.Items[i].Size == result.Items[j].Size {
			return result.Items[i].Path < result.Items[j].Path
		}
		return result.Items[i].Size > result.Items[j].Size
	})
}

func (f finding) origin() finding {
	for f.parent != nil {
		f = *f.parent
	}
	return f
}

// A cache entered from Storage has its own removal boundary, while retaining
// the full parent chain for filesystem identity checks.
func (f finding) removalRoot() finding {
	for f.parent != nil && f.parent.Kind == f.Kind {
		f = *f.parent
	}
	return f
}

func validateRoot(home string, f finding) error {
	if f.Path == filepath.Join(home, "Library/Caches/kitty") {
		return fmt.Errorf("Kitty cache is excluded")
	}
	if f.Kind == "Storage" && storageRoot(home, f.Path) {
		return nil
	}
	parent := filepath.Dir(f.Path)
	label, _ := cacheDetails(home, f.Path)
	allowed := label != ""
	for _, folder := range libraryFolders {
		if parent == filepath.Join(home, "Library", folder) {
			allowed = true
			if ideFolder(folder) {
				entries, err := os.ReadDir(parent)
				if err != nil {
					return err
				}
				if _, ok := olderIDEFolders(entries, folder)[filepath.Base(f.Path)]; !ok {
					return fmt.Errorf("IDE folder is no longer an older-version candidate; rescan")
				}
			}
		}
	}
	if !allowed || strings.HasPrefix(filepath.Base(f.Path), ".") {
		return fmt.Errorf("refusing path outside scanned Library categories")
	}
	return nil
}

// Ancestors must retain their identity, but sibling changes must not prevent
// removing a fully scanned child of an otherwise partially readable directory.
func validateLocation(home string, f finding) (os.FileInfo, error) {
	if err := validateRoot(home, f.origin()); err != nil {
		return nil, err
	}
	var target os.FileInfo
	for {
		if !filepath.IsAbs(f.Path) || f.Path != filepath.Clean(f.Path) {
			return nil, fmt.Errorf("invalid finding path")
		}
		parent := filepath.Dir(f.Path)
		if f.parent != nil && parent != f.parent.Path {
			return nil, fmt.Errorf("child is outside its detected directory")
		}
		real, err := filepath.EvalSymlinks(parent)
		if err != nil || real != parent {
			return nil, fmt.Errorf("parent changed or is symlinked; rescan")
		}
		now, err := os.Lstat(f.Path)
		if err != nil {
			return nil, err
		}
		if f.info == nil || now.Mode()&os.ModeSymlink != 0 || !os.SameFile(f.info, now) {
			return nil, fmt.Errorf("item or ancestor changed since scan; rescan")
		}
		if target == nil {
			target = now
		} else if !now.IsDir() {
			return nil, fmt.Errorf("ancestor is not a directory")
		}
		if f.parent == nil {
			return target, nil
		}
		f = *f.parent
	}
}

func validateFinding(home string, f finding) error {
	if f.Missing {
		return fmt.Errorf("remembered item is unavailable; refresh it before removal")
	}
	if err := validateRoot(home, f.removalRoot()); err != nil {
		return err
	}
	now, err := validateLocation(home, f)
	if err != nil {
		return err
	}
	if now.ModTime() != f.info.ModTime() || now.Size() != f.info.Size() {
		return fmt.Errorf("item changed since scan; rescan before removing")
	}
	if f.Incomplete {
		return fmt.Errorf("cannot remove an incompletely scanned item")
	}
	return nil
}

func validateRemoval(ctx context.Context, home string, roots []string, f finding) error {
	if err := validateFinding(home, f); err != nil {
		return err
	}
	relative, err := filepath.Rel(filepath.Join(home, "Library"), f.removalRoot().Path)
	if err != nil {
		return err
	}
	if ideFolder(filepath.Dir(relative)) {
		installed := inventory(ctx, roots)
		if installed.Incomplete || installed.IDEIncomplete {
			return fmt.Errorf("cannot confirm IDE ownership with incomplete inventory; rescan and review warnings")
		}
		if installed.IDEDirs[strings.ToLower(relative)] {
			return fmt.Errorf("an installed IDE now references this folder; rescan")
		}
	}
	// Inventory can take time; validate filesystem identity again before Trash.
	return validateFinding(home, f)
}

func trash(ctx context.Context, home string, roots []string, f finding) error {
	if err := validateRemoval(ctx, home, roots, f); err != nil {
		return err
	}
	// Pass the path as an argument, never interpolate it into AppleScript.
	// Finder supplies native Trash collision handling and Put Back metadata.
	out, err := exec.Command("/usr/bin/osascript", "-e", `on run argv
 tell application "Finder" to delete (POSIX file (item 1 of argv) as alias)
end run`, f.Path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Trash failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
