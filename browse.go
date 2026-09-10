package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func readDirectory(ctx context.Context, home string, dir finding) (finding, scanResult, error) {
	var result scanResult
	if ctx.Err() != nil {
		return dir, result, ctx.Err()
	}
	info, err := validateLocation(home, dir)
	if err != nil {
		return dir, result, err
	}
	if !info.IsDir() {
		return dir, result, fmt.Errorf("not a directory; use o to reveal this file in Finder")
	}
	dir.info = info
	entries, err := os.ReadDir(dir.Path)
	if err != nil {
		return dir, result, err
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return dir, result, ctx.Err()
		}
		path := filepath.Join(dir.Path, entry.Name())
		info, err := entry.Info()
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			result.Warnings = append(result.Warnings, "Skipped symlink or special file: "+path)
			continue
		}
		// Include hidden children: they were already counted inside the finding.
		child := finding{Path: path, Kind: dir.Kind, Reason: dir.Reason, info: info, parent: &dir}
		if dir.Kind == "Storage" && info.IsDir() {
			if label, reason := cacheDetails(home, path); label != "" {
				child.Kind, child.Label, child.Reason = "Cache", label, reason
			}
		}
		if err := result.addFinding(ctx, child); err != nil {
			return dir, result, err
		}
	}
	if _, err := validateLocation(home, dir); err != nil {
		return dir, scanResult{}, err
	}
	result.sort()
	return dir, result, ctx.Err()
}
