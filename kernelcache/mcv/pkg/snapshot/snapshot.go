/*
Copyright 2026 The KServe Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// Version is the current snapshot document version.
	Version = 1
	// DefaultPath is the default location shared by snapshot and create actions.
	DefaultPath = "/tmp/mcv/cache-snapshot.json"
)

// DefaultExcludedDirectories returns directory names that do not represent cache content.
func DefaultExcludedDirectories() []string {
	return []string{"dummy_cache"}
}

// Document contains directory snapshots for one or more cache roots.
type Document struct {
	Version int    `json:"version"`
	Roots   []Root `json:"roots"`
}

// Root contains the recursive directory names for one cache root.
type Root struct {
	Source              string   `json:"source"`
	ExcludedDirectories []string `json:"excludedDirectories,omitempty"`
	Directories         []string `json:"directories"`
}

// RootOptions configures a recursive directory snapshot for one cache root.
type RootOptions struct {
	Source              string
	ExcludedDirectories []string
}

// Delta contains newly added directories and the parent entries required in an OCI layer.
type Delta struct {
	Source              string
	AddedDirectories    []string
	ContentDirectories  []string
	RequiredDirectories []string
}

// Capture records recursive directory names without reading file contents.
func Capture(roots []string) (*Document, error) {
	options := make([]RootOptions, 0, len(roots))
	for _, root := range roots {
		options = append(options, RootOptions{Source: root})
	}
	return CaptureRoots(options)
}

// CaptureRoots records recursive directory names without reading file contents.
func CaptureRoots(roots []RootOptions) (*Document, error) {
	if len(roots) == 0 {
		return nil, errors.New("at least one snapshot root is required")
	}
	document := &Document{Version: Version, Roots: make([]Root, 0, len(roots))}
	seen := make(map[string]struct{}, len(roots))
	for _, options := range roots {
		root := filepath.Clean(options.Source)
		if root == "." || !filepath.IsAbs(root) {
			return nil, fmt.Errorf("snapshot root must be an absolute path: %q", options.Source)
		}
		if _, exists := seen[root]; exists {
			return nil, fmt.Errorf("duplicate snapshot root: %s", root)
		}
		seen[root] = struct{}{}
		excluded, err := normalizeExcludedDirectories(options.ExcludedDirectories)
		if err != nil {
			return nil, fmt.Errorf("invalid excluded directories for snapshot root %s: %w", root, err)
		}

		info, err := os.Lstat(root)
		if err != nil {
			return nil, fmt.Errorf("stat snapshot root %s: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("snapshot root is not a directory: %s", root)
		}

		directories := make([]string, 0)
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == root || !entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if excludedDirectory(relative, excluded) {
				return filepath.SkipDir
			}
			directories = append(directories, relative)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("walk snapshot root %s: %w", root, err)
		}
		sort.Strings(directories)
		document.Roots = append(document.Roots, Root{
			Source:              root,
			ExcludedDirectories: excluded,
			Directories:         directories,
		})
	}
	sort.Slice(document.Roots, func(i, j int) bool {
		return document.Roots[i].Source < document.Roots[j].Source
	})
	return document, nil
}

// Compare returns directories that were added after the previous snapshot.
func Compare(before, after *Document) ([]Delta, error) {
	if err := Validate(before); err != nil {
		return nil, fmt.Errorf("invalid previous snapshot: %w", err)
	}
	if err := Validate(after); err != nil {
		return nil, fmt.Errorf("invalid current snapshot: %w", err)
	}

	previous := make(map[string]Root, len(before.Roots))
	for _, root := range before.Roots {
		previous[root.Source] = root
	}

	deltas := make([]Delta, 0, len(after.Roots))
	for _, root := range after.Roots {
		previousRoot, exists := previous[root.Source]
		if !exists {
			return nil, fmt.Errorf("snapshot root was not present in previous snapshot: %s", root.Source)
		}
		if !sameDirectories(previousRoot.ExcludedDirectories, root.ExcludedDirectories) {
			return nil, fmt.Errorf("snapshot exclusions changed for root: %s", root.Source)
		}
		known := make(map[string]struct{}, len(previousRoot.Directories))
		for _, directory := range previousRoot.Directories {
			known[directory] = struct{}{}
		}
		added := make([]string, 0)
		for _, directory := range root.Directories {
			if _, exists := known[directory]; !exists {
				added = append(added, directory)
			}
		}
		if len(added) == 0 {
			continue
		}
		deltas = append(deltas, Delta{
			Source:              root.Source,
			AddedDirectories:    added,
			ContentDirectories:  contentDirectories(added),
			RequiredDirectories: requiredDirectories(added),
		})
	}
	return deltas, nil
}

func contentDirectories(added []string) []string {
	ordered := append([]string(nil), added...)
	sort.Slice(ordered, func(i, j int) bool {
		leftDepth := pathDepth(ordered[i])
		rightDepth := pathDepth(ordered[j])
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return ordered[i] < ordered[j]
	})
	content := make([]string, 0, len(added))
	for _, directory := range ordered {
		covered := false
		for _, parent := range content {
			if strings.HasPrefix(directory, parent+"/") {
				covered = true
				break
			}
		}
		if !covered {
			content = append(content, directory)
		}
	}
	return content
}

// Read loads and validates a snapshot document.
func Read(path string) (*Document, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the CLI accepts an explicit snapshot path.
	if err != nil {
		return nil, err
	}
	document := &Document{}
	if err := json.Unmarshal(data, document); err != nil {
		return nil, err
	}
	if err := Validate(document); err != nil {
		return nil, err
	}
	return document, nil
}

// Write atomically writes a validated snapshot document.
func Write(path string, document *Document) error {
	if err := Validate(document); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".mcv-snapshot-")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(err, file.Close())
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

// Validate checks the snapshot version, roots, and relative directory paths.
func Validate(document *Document) error {
	if document == nil {
		return errors.New("snapshot document is required")
	}
	if document.Version != Version {
		return fmt.Errorf("unsupported snapshot version: %d", document.Version)
	}
	if len(document.Roots) == 0 {
		return errors.New("snapshot must contain at least one root")
	}
	seenRoots := make(map[string]struct{}, len(document.Roots))
	for _, root := range document.Roots {
		if root.Source == "" || !filepath.IsAbs(root.Source) || filepath.Clean(root.Source) != root.Source {
			return fmt.Errorf("invalid snapshot root: %q", root.Source)
		}
		if _, exists := seenRoots[root.Source]; exists {
			return fmt.Errorf("duplicate snapshot root: %s", root.Source)
		}
		seenRoots[root.Source] = struct{}{}
		if _, err := normalizeExcludedDirectories(root.ExcludedDirectories); err != nil {
			return fmt.Errorf("invalid excluded directory for snapshot root %s: %w", root.Source, err)
		}
		seenDirectories := make(map[string]struct{}, len(root.Directories))
		for _, directory := range root.Directories {
			clean := filepath.ToSlash(filepath.Clean(directory))
			if directory == "" || clean == "." || clean != directory || filepath.IsAbs(directory) || directory == ".." || strings.HasPrefix(directory, "../") {
				return fmt.Errorf("invalid directory %q for snapshot root %s", directory, root.Source)
			}
			if _, exists := seenDirectories[directory]; exists {
				return fmt.Errorf("duplicate directory %q for snapshot root %s", directory, root.Source)
			}
			seenDirectories[directory] = struct{}{}
		}
	}
	return nil
}

func normalizeExcludedDirectories(directories []string) ([]string, error) {
	if len(directories) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(directories))
	seen := make(map[string]struct{}, len(directories))
	for _, directory := range directories {
		clean := filepath.ToSlash(filepath.Clean(directory))
		if directory == "" || clean == "." || clean != directory || filepath.IsAbs(directory) || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("invalid excluded directory %q", directory)
		}
		if _, exists := seen[clean]; exists {
			return nil, fmt.Errorf("duplicate excluded directory %q", directory)
		}
		seen[clean] = struct{}{}
		normalized = append(normalized, clean)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func excludedDirectory(relative string, excluded []string) bool {
	for _, directory := range excluded {
		if relative == directory || strings.HasPrefix(relative, directory+"/") {
			return true
		}
	}
	return false
}

func sameDirectories(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]struct{}, len(left))
	for _, directory := range left {
		values[directory] = struct{}{}
	}
	for _, directory := range right {
		if _, exists := values[directory]; !exists {
			return false
		}
	}
	return true
}

func requiredDirectories(added []string) []string {
	required := make(map[string]struct{})
	for _, directory := range added {
		for current := directory; current != "." && current != ""; current = filepath.ToSlash(filepath.Dir(current)) {
			required[current] = struct{}{}
		}
	}
	directories := make([]string, 0, len(required))
	for directory := range required {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(i, j int) bool {
		leftDepth := pathDepth(directories[i])
		rightDepth := pathDepth(directories[j])
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return directories[i] < directories[j]
	})
	return directories
}

func pathDepth(path string) int {
	depth := 1
	for _, character := range path {
		if character == '/' {
			depth++
		}
	}
	return depth
}
