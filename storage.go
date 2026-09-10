package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var storageFolders = []string{"Developer/Xcode", "Containers", "Group Containers"}
var storageTrees = []struct{ path, label string }{
	{"Library/Logs", "Diagnostic logs"},
	{".konan", "Kotlin/Native data"},
	{".lldb", "LLDB data"},
	{".m2", "Maven data"},
	{".gradle", "Gradle data"},
}

const storageWarning = "Danger: this is unverified application or system data. Removal can break apps or macOS services, lose settings or local artifacts, or trigger large downloads. Quit related apps and back up important data first."
const storageReason = storageWarning + " Size includes any nested caches shown separately."

func storageRoot(home, path string) bool {
	for _, tree := range storageTrees {
		if path == filepath.Join(home, tree.path) {
			return true
		}
	}
	for _, folder := range storageFolders {
		if filepath.Dir(path) == filepath.Join(home, "Library", folder) && !strings.HasPrefix(filepath.Base(path), ".") {
			return true
		}
	}
	return false
}

func (result *scanResult) scanStorage(ctx context.Context, home string) {
	for _, folder := range storageFolders {
		if ctx.Err() != nil {
			return
		}
		root := filepath.Join(home, "Library", folder)
		real, err := filepath.EvalSymlinks(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || real != root {
			result.Warnings = append(result.Warnings, "Skipped inaccessible or symlinked storage folder: "+root)
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", root, err))
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			path := filepath.Join(root, entry.Name())
			if label, _ := cacheDetails(home, path); label != "" {
				continue
			} // Already listed as a cache, e.g. DerivedData.
			label := filepath.Base(folder) + " / " + entry.Name()
			if err := result.addStorage(ctx, path, label); err != nil {
				return
			}
			if folder == "Containers" && entry.IsDir() {
				if err := result.addCache(ctx, home, filepath.Join(path, "Data/Library/Caches")); err != nil {
					return
				}
			}
		}
	}
	for _, tree := range storageTrees {
		if err := result.addStorage(ctx, filepath.Join(home, tree.path), tree.label); err != nil {
			return
		}
	}
}

func (result *scanResult) addStorage(ctx context.Context, path, label string) error {
	if ctx.Err() != nil {
		return ctx.Err()
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
	if err != nil || real != path || !info.IsDir() && !info.Mode().IsRegular() {
		result.Warnings = append(result.Warnings, "Skipped inaccessible, symlinked, or special storage entry: "+path)
		return nil
	}
	// ponytail: inclusive container sizes rewalk known cache trees; combine the walks if profiling shows this dominates scan time.
	return result.addFinding(ctx, finding{Path: path, Kind: "Storage", Label: label, Reason: storageReason, info: info})
}
