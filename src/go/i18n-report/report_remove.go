// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func runRemove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	stale := fs.Bool("stale", false, "Remove stale keys from all locale files (keys not in en-us.yaml)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	if *stale {
		return removeStaleKeys(root)
	}

	keys, err := readKeysFromStdin()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return fmt.Errorf("no valid keys provided on stdin")
	}

	keySet := make(map[string]bool, len(keys))
	for _, k := range keys {
		keySet[k] = true
	}

	targets, err := findTranslationFiles(root)
	if err != nil {
		return err
	}

	for _, path := range targets {
		removed, err := removeKeysFromFile(path, keySet)
		if err != nil {
			return err
		}
		if removed > 0 {
			relPath, _ := filepath.Rel(root, path)
			fmt.Fprintf(os.Stderr, "Removed %d %s from %s\n", removed, plural(removed, "key"), relPath)
		}
	}

	return nil
}

// removeStaleKeys removes keys from each non-en-us locale file that
// do not exist in en-us.yaml.
func removeStaleKeys(root string) error {
	enPath := translationsPath(root, "en-us.yaml")
	enKeys, err := loadYAMLFlat(enPath)
	if err != nil {
		return err
	}

	targets, err := findTranslationFiles(root)
	if err != nil {
		return err
	}

	for _, path := range targets {
		if filepath.Base(path) == "en-us.yaml" {
			continue
		}

		localeKeys, err := loadYAMLFlat(path)
		if err != nil {
			return err
		}

		stale := computeStale(enKeys, localeKeys)
		if len(stale) == 0 {
			continue
		}
		staleKeys := make(map[string]bool, len(stale))
		for _, k := range stale {
			staleKeys[k] = true
		}

		removed, err := removeKeysFromFile(path, staleKeys)
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(root, path)
		fmt.Fprintf(os.Stderr, "Removed %d stale %s from %s\n", removed, plural(removed, "key"), relPath)
	}

	return nil
}

// readKeysFromStdin reads dotted translation keys from stdin, one per line.
// Lines that are not valid dotted keys are skipped, so the output of
// `unused` or `stale` can be piped directly.
func readKeysFromStdin() ([]string, error) {
	var keys []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		key := strings.TrimSpace(scanner.Text())
		if isValidDottedKey(key) {
			keys = append(keys, key)
		}
	}
	return keys, scanner.Err()
}

// findTranslationFiles returns paths to all YAML files in the translations
// directory. Matches any .yaml file; sufficient for current naming conventions.
func findTranslationFiles(root string) ([]string, error) {
	dir := filepath.Join(root, translationsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	return paths, nil
}

// removeKeysFromFile removes the given dotted keys from a YAML file,
// pruning empty parent nodes. Returns the number of keys removed.
// It deletes only the removed keys' lines from the original text, so
// every other key keeps its hand formatting.
func removeKeysFromFile(path string, keys map[string]bool) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("parsing %s: %w", path, err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return 0, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return 0, nil
	}
	if err := validateNoAliases(root); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}

	lines := strings.SplitAfter(string(data), "\n")
	deleted := make([]bool, len(lines))
	removed := 0
	for key := range keys {
		parts, err := splitKeyPath(key)
		if err != nil {
			return 0, fmt.Errorf("%s: invalid key %q: %w", path, key, err)
		}
		keyNode, err := removeKeyFromNode(root, parts)
		if err != nil {
			return 0, fmt.Errorf("%s: key %q: %w", path, key, err)
		}
		if keyNode != nil {
			markKeyLines(lines, deleted, keyNode)
			removed++
		}
	}

	if removed == 0 {
		return 0, nil
	}
	dropDoubledBlankLines(lines, deleted)

	var buf strings.Builder
	for i, line := range lines {
		if !deleted[i] {
			buf.WriteString(line)
		}
	}

	if err := writeFileAtomic(path, []byte(buf.String())); err != nil {
		return 0, fmt.Errorf("writing %s: %w", path, err)
	}

	return removed, nil
}

// removeKeyFromNode removes a dotted key path from a mapping node,
// pruning empty parents. It returns the key node of the outermost pair
// removed (the pruned parent, if any), or nil if the key was not found.
// It returns an error for a key inside a flow mapping, since that key
// shares its line with its siblings and the line cannot be deleted alone.
func removeKeyFromNode(node *yaml.Node, parts []string) (*yaml.Node, error) {
	if node.Kind != yaml.MappingNode || len(parts) == 0 {
		return nil, nil
	}

	for i := 0; i < len(node.Content)-1; i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		if keyNode.Value != parts[0] {
			continue
		}

		if len(parts) == 1 {
			if node.Style&yaml.FlowStyle != 0 {
				return nil, fmt.Errorf("cannot remove a key from a flow mapping (line %d)", keyNode.Line)
			}
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return keyNode, nil
		}

		// Recurse into nested mapping.
		removedKey, err := removeKeyFromNode(valNode, parts[1:])
		if err != nil || removedKey == nil {
			return nil, err
		}
		// Prune empty parent.
		if valNode.Kind == yaml.MappingNode && len(valNode.Content) == 0 {
			if node.Style&yaml.FlowStyle != 0 {
				return nil, fmt.Errorf("cannot remove a key from a flow mapping (line %d)", keyNode.Line)
			}
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return keyNode, nil
		}
		return removedKey, nil
	}
	return nil, nil
}

// markKeyLines marks for deletion the line of keyNode, its value (every
// following line that is blank or indented deeper than the key, minus the
// blank lines trailing it), and its head comment (the contiguous comment
// lines directly above it at the key's indentation).
func markKeyLines(lines []string, deleted []bool, keyNode *yaml.Node) {
	keyIdx := keyNode.Line - 1
	indent := keyNode.Column - 1
	deleted[keyIdx] = true

	end := keyIdx
	for j := keyIdx + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "" {
			continue
		}
		if lineIndent(lines[j]) <= indent {
			break
		}
		end = j
	}
	for j := keyIdx + 1; j <= end; j++ {
		deleted[j] = true
	}

	for j := keyIdx - 1; j >= 0; j-- {
		trimmed := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(trimmed, "#") || lineIndent(lines[j]) != indent {
			break
		}
		deleted[j] = true
	}
}

// lineIndent returns the number of leading spaces on a line.
func lineIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// dropDoubledBlankLines marks the blank line after a deleted block for
// deletion when a blank line also precedes the block, so removing a group
// that sat between two blank lines leaves one blank line, not two.
func dropDoubledBlankLines(lines []string, deleted []bool) {
	for start := 0; start < len(lines); start++ {
		if !deleted[start] {
			continue
		}
		end := start
		for end+1 < len(lines) && deleted[end+1] {
			end++
		}
		if start > 0 && end+1 < len(lines) && isBlankLine(lines[start-1]) && isBlankLine(lines[end+1]) {
			deleted[end+1] = true
			end++
		}
		start = end
	}
}

// isBlankLine reports whether line is an empty or whitespace-only line. The
// empty string after a file's final newline is not a line.
func isBlankLine(line string) bool {
	return strings.HasSuffix(line, "\n") && strings.TrimSpace(line) == ""
}
