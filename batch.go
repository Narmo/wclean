package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type batchItem struct {
	finding finding
	missing bool
	err     error
	outcome string
}

func prepareBatch(ctx context.Context, home string, selected []finding) []batchItem {
	// An explicitly selected parent subsumes its descendants, regardless of size.
	selectedPaths := map[string]bool{}
	for _, f := range selected {
		selectedPaths[f.Path] = true
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Path < selected[j].Path })
	var result []batchItem
	seen := map[string]bool{}
	for _, f := range selected {
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		covered := false
		for parent := filepath.Dir(f.Path); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
			if selectedPaths[parent] {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		item := batchItem{finding: f}
		if ctx.Err() != nil {
			item.err = ctx.Err()
		} else if f.Kind == "Cache" {
			resolved, err := resolveCache(ctx, home, f.Path)
			if err == nil {
				item.finding = resolved
			} else {
				item.err = err
			}
		} else {
			item.err = validateFinding(home, f)
		}
		if errors.Is(item.err, os.ErrNotExist) {
			item.missing = true
			item.err = nil
		}
		result = append(result, item)
	}
	return result
}

func executeBatchItem(ctx context.Context, home string, roots []string, item batchItem) (missing bool, err error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if item.err != nil {
		return false, item.err
	}
	if item.missing {
		return true, nil
	}
	err = trash(ctx, home, roots, item.finding)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, err
}

func cleanRemembered(ctx context.Context, home string, in io.Reader, out io.Writer, yes bool) error {
	memory, err := loadMemory(home)
	if err != nil {
		return err
	}
	selected := make([]finding, 0, len(memory))
	for path := range memory {
		selected = append(selected, finding{Path: path, Kind: "Cache"})
	}
	if len(selected) == 0 {
		fmt.Fprintln(out, "No remembered caches. Remove a cache in the TUI first.")
		return nil
	}
	items := prepareBatch(ctx, home, selected)
	available, missing, failures := 0, 0, 0
	for _, item := range items {
		switch {
		case item.err != nil:
			failures++
			fmt.Fprintf(out, "ERROR %q: %s\n", item.finding.Path, safe(item.err.Error()))
		case item.missing:
			missing++
			fmt.Fprintf(out, "MISSING %q (retained for future batches)\n", item.finding.Path)
		default:
			available++
			fmt.Fprintf(out, "%s  %q\n", human(item.finding.Size), item.finding.Path)
		}
	}
	if available > 0 {
		if !yes {
			fmt.Fprintf(out, "Move %d remembered cache(s) to Trash? Close their apps first. [y/N] ", available)
			answer, err := bufio.NewReader(in).ReadString('\n')
			if err != nil && err != io.EOF {
				return err
			}
			if strings.TrimSpace(answer) != "y" {
				fmt.Fprintln(out, "Cancelled. Nothing removed.")
				return nil
			}
		}
		for _, item := range items {
			if item.err != nil || item.missing {
				continue
			}
			absent, err := executeBatchItem(ctx, home, nil, item)
			switch {
			case err != nil:
				failures++
				fmt.Fprintf(out, "ERROR %q: %s\n", item.finding.Path, safe(err.Error()))
			case absent:
				missing++
				fmt.Fprintf(out, "MISSING %q (retained)\n", item.finding.Path)
			default:
				fmt.Fprintf(out, "TRASHED %q\n", item.finding.Path)
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	// The CLI never rewrites memory: missing and successfully removed paths stay saved.
	fmt.Fprintf(out, "%d missing; %d error(s). Remembered paths retained.\n", missing, failures)
	if failures > 0 {
		return fmt.Errorf("batch cleanup had %d error(s)", failures)
	}
	return nil
}
