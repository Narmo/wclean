package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type appInventory struct {
	IDs                       map[string]bool
	IDEDirs                   map[string]bool // Paths relative to ~/Library, not app display names.
	Warnings                  []string
	Incomplete, IDEIncomplete bool
}

var knownIDE = regexp.MustCompile(`^(com\.google\.android\.studio(?:\.canary)?|com\.jetbrains\.(?:intellij|pycharm|webstorm|phpstorm|clion|goland|rider|datagrip|rubymine|rustrover|aqua|dataspell)(?:\.ce|\.ultimate|\.eap)?)$`)

func inventory(ctx context.Context, roots []string) appInventory {
	result := appInventory{IDs: map[string]bool{}, IDEDirs: map[string]bool{}}
	visited := map[string]bool{}
	plists := map[string]bool{}
	fail := func(path string, err error) {
		result.Incomplete = true
		result.Warnings = append(result.Warnings, fmt.Sprintf("App inventory: %s: %v", path, err))
	}
	var walk func(string)
	walk = func(root string) {
		if ctx.Err() != nil {
			result.Incomplete = true
			return
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			fail(root, err)
			return
		}
		real, err = filepath.Abs(real)
		if err != nil {
			fail(root, err)
			return
		}
		info, err := os.Stat(real)
		if err != nil {
			fail(root, err)
			return
		}
		if !info.IsDir() {
			fail(root, fmt.Errorf("application search location is not a directory"))
			return
		}
		err = filepath.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				fail(path, err)
				return nil
			}
			app := strings.EqualFold(filepath.Ext(d.Name()), ".app")
			if d.Type()&fs.ModeSymlink != 0 {
				// Follow application aliases only, not arbitrary links into the filesystem.
				if app || d.Name() == "WrappedBundle" {
					walk(path)
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if visited[path] {
				return filepath.SkipDir
			}
			visited[path] = true
			if !app {
				return nil
			}
			plist, err := bundlePlist(path)
			if err != nil {
				fail(path, err)
				return nil
			}
			id := ""
			if plist == "" {
				result.Warnings = append(result.Warnings, "No supported bundle metadata; skipped: "+path)
			} else {
				canonical, err := filepath.EvalSymlinks(plist)
				if err != nil {
					fail(plist, err)
					return nil
				}
				if !plists[canonical] {
					plists[canonical] = true
					data, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-expect", "string", "-o", "-", canonical).CombinedOutput()
					id = strings.ToLower(strings.TrimSpace(string(data)))
					if err != nil {
						fail(canonical, fmt.Errorf("bundle ID: %w: %s", err, strings.TrimSpace(string(data))))
					} else if id == "" {
						fail(canonical, fmt.Errorf("empty bundle ID"))
					} else {
						result.IDs[id] = true
					}
				}
			}
			result.readIDE(path, id)
			// Continue through embedded apps, including flat iOS bundles in developer tools.
			return nil
		})
		if err != nil {
			fail(real, err)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	return result
}

func bundlePlist(app string) (string, error) {
	for _, relative := range []string{"Contents/Info.plist", "Info.plist", "WrappedBundle/Info.plist", "WrappedBundle/Contents/Info.plist"} {
		path := filepath.Join(app, relative)
		_, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		return path, nil
	}
	return "", nil
}

func (result *appInventory) readIDE(app, id string) {
	path := filepath.Join(app, "Contents/Resources/product-info.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && !knownIDE.MatchString(id) {
		return
	}
	var product struct {
		DataDirectoryName string `json:"dataDirectoryName"`
		ProductCode       string `json:"productCode"`
		Launch            []struct {
			AdditionalJvmArguments []string `json:"additionalJvmArguments"`
		} `json:"launch"`
	}
	if err == nil {
		err = json.Unmarshal(data, &product)
	}
	name := product.DataDirectoryName
	if err == nil && (name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\")) {
		err = fmt.Errorf("invalid or missing dataDirectoryName")
	}
	// ponytail: standard metadata paths only; custom config overrides need a properties/launcher resolver before classifying IDE data.
	for _, launch := range product.Launch {
		for _, arg := range launch.AdditionalJvmArguments {
			if strings.HasPrefix(arg, "-Didea.config.path=") || strings.HasPrefix(arg, "-Didea.paths.selector=") && strings.TrimPrefix(arg, "-Didea.paths.selector=") != name {
				err = fmt.Errorf("custom IDE configuration path requires manual review")
			}
		}
	}
	if err != nil {
		result.IDEIncomplete = true
		result.Warnings = append(result.Warnings, fmt.Sprintf("IDE metadata: %s: %v", path, err))
		return
	}
	vendor := "JetBrains"
	if product.ProductCode == "AI" || strings.HasPrefix(name, "AndroidStudio") {
		vendor = "Google"
	}
	result.IDEDirs[strings.ToLower(filepath.Join("Application Support", vendor, name))] = true
}
