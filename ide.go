package main

import (
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var ideVersion = regexp.MustCompile(`^(IntelliJIdea|IdeaIC|PyCharm|PyCharmCE|WebStorm|PhpStorm|CLion|GoLand|Rider|DataGrip|RubyMine|RustRover|Aqua|DataSpell|AndroidStudio|AndroidStudioPreview)([0-9]{4}\.[0-9]+(?:\.[0-9]+)*)$`)

func ideFolder(folder string) bool {
	return folder == "Application Support/JetBrains" || folder == "Application Support/Google"
}

// Only compare versions within the same product/edition/channel. A newer folder
// is evidence for review, not proof that the older IDE is no longer in use.
func olderIDEFolders(entries []fs.DirEntry, folder string) map[string]string {
	versions := map[string][]int{}
	products := map[string]string{}
	newest := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		match := ideVersion.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		android := strings.HasPrefix(match[1], "AndroidStudio")
		if android != (folder == "Application Support/Google") {
			continue
		}
		var version []int
		valid := true
		for _, part := range strings.Split(match[2], ".") {
			n, err := strconv.Atoi(part)
			if err != nil {
				valid = false
				break
			}
			version = append(version, n)
		}
		if !valid {
			continue
		}
		// Treat trailing zero components as equivalent, e.g. 2025.1 and 2025.1.0.
		for len(version) > 0 && version[len(version)-1] == 0 {
			version = version[:len(version)-1]
		}
		versions[entry.Name()] = version
		products[entry.Name()] = match[1]
		if previous, ok := newest[match[1]]; !ok || slices.Compare(version, versions[previous]) > 0 {
			newest[match[1]] = entry.Name()
		}
	}
	older := map[string]string{}
	for name, version := range versions {
		latest := newest[products[name]]
		if slices.Compare(version, versions[latest]) < 0 {
			older[name] = latest
		}
	}
	return older
}
