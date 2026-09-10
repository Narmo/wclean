package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

func withinPath(parent, path string) bool {
	return path == parent || strings.HasPrefix(path, parent+string(filepath.Separator))
}
func memoryPath(home string) string {
	return filepath.Join(home, "Library/Application Support/wclean/remembered-caches.json")
}

// Persistence never grants removal permission: every path must still belong to
// a current, explicit cache location. Storage/leftovers cannot become auto-clean rules.
func cacheRootFor(home, path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("invalid remembered cache path")
	}
	base := filepath.Join(home, "Library/Caches")
	if strings.HasPrefix(path, base+string(filepath.Separator)) {
		name := strings.Split(strings.TrimPrefix(path, base+string(filepath.Separator)), string(filepath.Separator))[0]
		if name != "kitty" && !strings.HasPrefix(name, ".") {
			return filepath.Join(base, name), nil
		}
	}
	for _, target := range cacheTargets {
		root := filepath.Join(home, "Library", target.path)
		if withinPath(root, path) {
			return root, nil
		}
	}
	relative, err := filepath.Rel(filepath.Join(home, "Library/Containers"), path)
	parts := strings.Split(relative, string(filepath.Separator))
	if err == nil && len(parts) >= 4 {
		root := filepath.Join(home, "Library/Containers", filepath.Join(parts[:4]...))
		if label, _ := cacheDetails(home, root); label != "" && withinPath(root, path) {
			return root, nil
		}
	}
	return "", fmt.Errorf("not a supported cache path: %s", path)
}

func loadMemory(home string) (map[string]bool, error) {
	remembered := map[string]bool{}
	path := memoryPath(home)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return remembered, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("cache preferences are not a regular file")
	}
	real, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		return remembered, nil
	}
	if err != nil {
		return nil, err
	}
	if real != path {
		return nil, fmt.Errorf("refusing symlinked cache preferences: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 || !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid or oversized cache preferences")
	}
	var paths []string
	if err := json.Unmarshal(data, &paths); err != nil {
		return nil, fmt.Errorf("cannot read cache preferences: %w", err)
	}
	for _, path := range paths {
		if _, err := cacheRootFor(home, path); err != nil {
			return nil, err
		}
		remembered[path] = true
	}
	return remembered, nil
}

func saveMemory(home string, remembered map[string]bool) error {
	paths := make([]string, 0, len(remembered))
	for path := range remembered {
		if _, err := cacheRootFor(home, path); err != nil {
			return err
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	data, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > 1<<20 {
		return fmt.Errorf("cache preferences exceed 1 MiB")
	}
	path := memoryPath(home)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil || real != dir {
		return fmt.Errorf("refusing redirected preferences directory")
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("preferences are not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(dir, ".remembered-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// ponytail: atomic replacement, one editing session at a time; add locking/merge for concurrent preference editors.
	return os.Rename(file.Name(), path)
}

func resolveCache(ctx context.Context, home, path string) (finding, error) {
	root, err := cacheRootFor(home, path)
	if err != nil {
		return finding{}, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return finding{}, err
	}
	label, reason := cacheDetails(home, root)
	if label != "" && !info.IsDir() {
		return finding{}, fmt.Errorf("cache location is no longer a directory: %s", root)
	}
	if reason == "" {
		reason = "Remembered cache; quit the owning app first. It may contain offline data or be regenerated."
	}
	f := finding{Path: root, Kind: "Cache", Label: label, Reason: reason, info: info}
	if root != path {
		relative, _ := filepath.Rel(root, path)
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			if ctx.Err() != nil {
				return finding{}, ctx.Err()
			}
			if !f.info.IsDir() {
				return finding{}, fmt.Errorf("cache ancestor is not a directory")
			}
			if _, err := validateLocation(home, f); err != nil {
				return finding{}, err
			}
			next := filepath.Join(f.Path, part)
			info, err := os.Lstat(next)
			if err != nil {
				return finding{}, err
			}
			parent := f
			f = finding{Path: next, Kind: "Cache", Reason: reason, info: info, parent: &parent}
		}
	}
	if _, err := validateLocation(home, f); err != nil {
		return finding{}, err
	}
	if !f.info.IsDir() && !f.info.Mode().IsRegular() {
		return finding{}, fmt.Errorf("cache target is a special file")
	}
	var measured scanResult
	if err := measured.addFinding(ctx, f); err != nil {
		return finding{}, err
	}
	f = measured.Items[0]
	if err := validateFinding(home, f); err != nil {
		return f, err
	}
	return f, nil
}

func (m model) selectionMark(path string) string {
	state := 0
	consider := func(selected string) {
		if withinPath(selected, path) {
			state = 2
		} else if state == 0 && withinPath(path, selected) {
			state = 1
		}
	}
	for selected := range m.checked {
		consider(selected)
	}
	for selected := range m.remembered {
		consider(selected)
	}
	return []string{"[ ]", "[~]", "[x]"}[state]
}

func (m *model) persistMemory() error {
	if m.memoryError != nil {
		return fmt.Errorf("preferences were not loaded; existing file left unchanged: %w", m.memoryError)
	}
	if err := saveMemory(m.home, m.remembered); err != nil {
		return err
	}
	m.memoryDirty = false
	return nil
}
func (m *model) rememberCache(f finding) error {
	if f.Kind != "Cache" || f.info == nil || f.Missing || m.remembered[f.Path] && !m.memoryDirty {
		return nil
	}
	if _, err := cacheRootFor(m.home, f.Path); err != nil {
		return err
	}
	if m.remembered == nil {
		m.remembered = map[string]bool{}
	}
	m.remembered[f.Path] = true
	m.memoryDirty = true
	return m.persistMemory()
}
func (m *model) toggleChecked(f finding) {
	if m.checked == nil {
		m.checked = map[string]finding{}
	}
	if m.selectionMark(f.Path) == "[ ]" {
		m.checked[f.Path] = f
		m.status = "Checked for this batch. Successful cache removals are remembered."
		return
	}
	affected := func(path string) bool { return withinPath(f.Path, path) || withinPath(path, f.Path) }
	for path := range m.checked {
		if affected(path) {
			delete(m.checked, path)
		}
	}
	changed := false
	for path := range m.remembered {
		if affected(path) {
			delete(m.remembered, path)
			changed = true
		}
	}
	m.status = "Unchecked, including any enclosing selection."
	if changed {
		m.memoryDirty = true
		if err := m.persistMemory(); err != nil {
			m.statusError = true
			m.status = "Unchecked for this run, but could not save: " + err.Error()
		} else {
			m.status += " Removed from remembered batches."
		}
	}
	m.result.Items = slices.DeleteFunc(m.result.Items, func(item finding) bool { return item.Missing && !m.remembered[item.Path] })
	m.cursor = max(0, min(m.cursor, len(m.visible())-1))
}

func (m model) selectedItems() []finding {
	items := map[string]finding{}
	for path := range m.remembered {
		items[path] = finding{Path: path, Kind: "Cache"}
	}
	for path, f := range m.checked {
		items[path] = f
	}
	result := make([]finding, 0, len(items))
	for _, f := range items {
		result = append(result, f)
	}
	return result
}

func (m model) findingAt(path string) (finding, bool) {
	for _, items := range [][]finding{m.children, m.result.Items} {
		for _, f := range items {
			if f.Path == path {
				return f, true
			}
		}
	}
	f, ok := m.checked[path]
	return f, ok
}

func (m model) addMissingMemory(result *scanResult) {
	for path := range m.remembered {
		if slices.ContainsFunc(result.Items, func(f finding) bool { return f.Path == path }) {
			continue
		}
		info, err := os.Lstat(path)
		if err == nil && info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		reason := "Missing; remembered for future batches. Uncheck to forget this path."
		if err != nil && !os.IsNotExist(err) || err == nil {
			reason = "Remembered path is currently unavailable or symlinked; uncheck to forget it."
		}
		result.Items = append(result.Items, finding{Path: path, Kind: "Cache", Missing: true, Reason: reason})
	}
	result.sort()
}
