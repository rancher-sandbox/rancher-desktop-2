// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command generate-golang-expect generates spelling exclusions.
// This should be run at the top level.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	expectFile = ".github/actions/spelling/expect/golang-generated.txt"
)

type checker struct {
	// Words that will go in the "expect" list
	expect map[string]bool
	// Words that end with an "s" but are singular
	singular map[string]bool
}

// Process one golang source file, updating `c.expect` with new words found via
// the imports in the given file.  This is a `fs.WalkDirFunc`.
// Currently this only supports the `import ()` form (i.e. multi-line import
// declaration).
func (c *checker) checkFile(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if d.Type() != 0 {
		return nil
	}
	if !strings.HasSuffix(d.Name(), ".go") {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	matcher := regexp.MustCompile(`^\s*(?:import\s+)?(?:(\S+)\s+)?"([^"]+)"$`)
	inImport := false // Set to true when we're in the import section.
	for scanner.Scan() {
		if !inImport {
			inImport = strings.HasPrefix(scanner.Text(), "import ")
		}
		if !inImport {
			continue
		}
		if strings.HasPrefix(scanner.Text(), ")") {
			break // End of imports
		}
		matches := matcher.FindStringSubmatch(scanner.Text())
		if matches == nil {
			continue
		}
		// Split the import path by dots, slashes, and other non-alpha-numeric
		// separators.
		words := strings.FieldsFunc(strings.ToLower(matches[2]), func(r rune) bool {
			return !unicode.In(r, unicode.N, unicode.L)
		})
		// wordMap is the set of words in this import; this is used to check if
		// the explicit package name is valid.
		wordMap := make(map[string]bool)
		for _, word := range words {
			wordMap[word] = true
			wordParts := strings.FieldsFunc(word, unicode.IsNumber)
			if len(wordParts) > 1 {
				// This word contains numbers; register each part separately, as
				// check-spelling splits on numbers.
				for _, part := range wordParts {
					c.expect[part] = true
				}
			} else if _, ok := c.singular[word]; !ok && len(word) > 3 && strings.HasSuffix(word, "s") {
				// This is (probably) a plural word; add it as well as the singular form to the known list.
				// But only add the plural to the known-words list (because it's actually in use).
				wordMap[word[:len(word)-1]] = true
				c.expect[word] = true
			} else {
				// This is a normal word, possibly starting or ending with digits; add it.
				c.expect[wordParts[0]] = true
			}
		}
		// Sometimes the explicit package name has abbreviations; this is set of
		// them so that we can match them up.  The key is a set of space-separated
		// strings; we add the value if all of the words in the key are found.
		extraMappings := map[string]string{
			"apiserver":              "genericapi", // spellchecker:ignore
			"authentication":         "authenticator",
			"controller":             "ctrl",
			"certificate":            "cert",
			"kubernetes":             "kube",
			"rancher desktop daemon": "rdd",
		}
	extraMappingsLoop:
		for k, v := range extraMappings {
			for word := range strings.FieldsSeq(k) {
				if _, ok := wordMap[word]; !ok {
					continue extraMappingsLoop
				}
			}
			wordMap[v] = true
		}

		// For checking the package name, use the set of words.
		words = slices.Collect(maps.Keys(wordMap))
		// Sort word by longest to shortest
		slices.SortFunc(words, func(a, b string) int {
			return len(b) - len(a)
		})
		packageName := matches[1]
		if len(packageName) < 2 {
			continue // No explicit package name, or imported for side effects
		}
		for packageName != "" {
			found := false
			for _, word := range words {
				if strings.HasPrefix(packageName, word) {
					packageName = packageName[len(word):]
					found = true
					break
				}
			}
			if !found {
				break
			}
		}
		if packageName == "" {
			// check-spelling ignores digits too; do that here.
			for word := range strings.FieldsFuncSeq(matches[1], unicode.IsNumber) {
				c.expect[word] = true
			}
		} else {
			log.Printf("alias %q not found in %v\n", matches[1], words)
		}
	}

	return nil
}

// Generate a set of words in the system dictionary, if available.  This is used
// to avoid unnecessarily adding entries to the list.
func (c *checker) getWords() (map[string]bool, error) {
	f, err := os.Open("/usr/share/dict/words")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	words := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	scanner.Split(bufio.ScanWords)
	for scanner.Scan() {
		words[scanner.Text()] = true
	}
	// Also add every single-letter words, since those get ignored anyway.
	for c := 'a'; c <= 'z'; c++ {
		words[string(c)] = true
	}
	return words, nil
}

// Run the spell checker word list generator.
func (c *checker) run() error {
	dictionaryWords, err := c.getWords()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to find working directory: %w", err)
	}
	if err := fs.WalkDir(os.DirFS(cwd), ".", c.checkFile); err != nil {
		return err
	}
	f, err := os.Create(expectFile)
	if err != nil {
		return err
	}
	// Walk the words (sorted in ASCII order), and emit words to the word list.
	for _, word := range slices.Sorted(maps.Keys(c.expect)) {
		if _, ok := dictionaryWords[word]; ok {
			continue // Skip known dictionary words.
		}
		if _, ok := c.singular[word]; !ok && strings.HasSuffix(word, "s") {
			singular := word[:len(word)-1]
			if _, ok := dictionaryWords[singular]; ok {
				// If the singular form exists in the dictionary, skip the plural form.
				continue
			}
			if _, ok := c.expect[singular]; ok {
				// The singular form exists in a word we found.
				continue
			}
		}
		if _, err := f.WriteString(word + "\n"); err != nil {
			// Something went wrong writing the word list; remove it.
			_ = f.Close()
			_ = os.Remove(expectFile)
			return err
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(expectFile)
		return err
	}
	return nil
}

func main() {
	c := checker{
		expect: make(map[string]bool),
		singular: map[string]bool{
			"kubernetes": true,
			"logrus":     true,
			"prometheus": true,
			"readiness":  true,
		},
	}
	if err := c.run(); err != nil {
		log.Fatal(err)
	}
}
