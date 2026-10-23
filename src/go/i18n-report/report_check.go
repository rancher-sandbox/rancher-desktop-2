// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
)

// runCheck is the CI gate. Bare `check` runs the locale-independent source
// gate (unused + undefined keys). With --locale it also verifies the locale
// registration surfaces and runs the per-locale checks; --locale=all covers
// every translation file on disk. --strict adds the completeness checks
// (no missing, no drifted keys). No configured job passes it; PR CI runs
// the default structural set.
func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	locale := fs.String("locale", "", "Target locale, or 'all' (omit for the source-only gate)")
	strict := fs.Bool("strict", false, "Require complete translations: no missing, no drifted keys (requires --locale)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *strict && *locale == "" {
		return fmt.Errorf("--strict requires --locale")
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	available, err := translationLocales(root)
	if err != nil {
		return err
	}

	// Reject a bad single --locale before the source gate prints, so it is an
	// operational error like drift's rather than a per-locale finding surfaced
	// after a partial pass. --locale=all needs no such check.
	if *locale != "" && *locale != localeAll {
		if *locale == sourceLocale {
			return fmt.Errorf("locale %q is the source locale; per-locale checks do not apply", *locale)
		}
		if !slices.Contains(available, *locale) {
			return fmt.Errorf("no translation file for locale %q", *locale)
		}
	}

	// The source gate always runs. An operational failure (unreadable file)
	// aborts immediately; findings are collected and combined with the
	// per-locale results below.
	sourceErr := reportCheckSource(os.Stdout, root)
	if sourceErr != nil && !errors.Is(sourceErr, errFindings) {
		return sourceErr
	}

	if *locale == "" {
		return sourceErr
	}

	failed := sourceErr != nil

	fmt.Println()
	if regErr := reportCheckRegistration(os.Stdout, root, available); regErr != nil {
		if !errors.Is(regErr, errFindings) {
			return regErr
		}
		failed = true
	}

	locales := available
	if *locale != localeAll {
		locales = []string{*locale}
	}

	for _, loc := range locales {
		fmt.Println()
		if err := reportCheckLocale(os.Stdout, root, loc, *strict); err != nil {
			if !errors.Is(err, errFindings) {
				return err
			}
			failed = true
		}
	}

	if failed {
		return findingsError("check failed")
	}
	return nil
}

// reportCheckSource runs the locale-independent source gate: keys defined in
// en-us.yaml but referenced nowhere (unused) and keys referenced in source
// but missing from en-us.yaml (undefined). Undefined keys render as "%key%"
// placeholders in every locale, so they fail regardless of locale.
func reportCheckSource(w io.Writer, root string) error {
	enPath := translationsPath(root, "en-us.yaml")
	enKeys, err := loadYAMLFlat(enPath)
	if err != nil {
		return err
	}

	refs, err := findKeyReferences(root, enKeys)
	if err != nil {
		return err
	}
	unusedCount := len(computeUnused(enKeys, refs))

	undefined, err := findUndefinedKeys(root, enKeys)
	if err != nil {
		return err
	}

	fmt.Fprintln(w, "Source checks:")
	passed := true
	printResult := func(label string, count int) {
		status := "OK"
		if count > 0 {
			status = "FAIL"
			passed = false
		}
		fmt.Fprintf(w, "  %-30s %3d  %s\n", label+":", count, status)
	}

	printResult("unused keys", unusedCount)
	printResult("undefined keys", len(undefined))

	if passed {
		fmt.Fprintln(w, "Source checks passed.")
		return nil
	}
	return findingsError("source checks failed")
}

// reportCheckRegistration checks that every locale registration surface
// agrees with the translation files on disk: the LocaleString union in
// translationLoader.ts, the Locale union in settings.ts, and the locale.*
// display-name keys in en-us.yaml.
func reportCheckRegistration(w io.Writer, root string, locales []string) error {
	// The expected locales: the source locale and every translation file on disk.
	expected := map[string]bool{sourceLocale: true}
	for _, code := range locales {
		expected[code] = true
	}

	var problems []string

	// Both TypeScript locale unions must list exactly the expected locales.
	unions := []struct{ path, name string }{
		{filepath.Join("pkg", "rancher-desktop", "utils", "translationLoader.ts"), "LocaleString"},
		{filepath.Join("pkg", "rancher-desktop", "config", "settings.ts"), "Locale"},
	}
	for _, u := range unions {
		members, err := parseTypeUnion(filepath.Join(root, u.path), u.name)
		if err != nil {
			return err
		}
		file := filepath.Base(u.path)
		for code := range expected {
			if !members[code] {
				problems = append(problems, fmt.Sprintf("  locale %q missing from %s type %s", code, file, u.name))
			}
		}
		for code := range members {
			if !expected[code] {
				problems = append(problems, fmt.Sprintf("  %s type %s has %q with no translation file", file, u.name, code))
			}
		}
	}

	// Every locale needs a display name for the language picker.
	enKeys, err := loadYAMLFlat(translationsPath(root, "en-us.yaml"))
	if err != nil {
		return fmt.Errorf("reading en-us.yaml: %w", err)
	}
	for _, code := range append([]string{sourceLocale}, locales...) {
		if _, exists := enKeys["locale."+code]; !exists {
			problems = append(problems, fmt.Sprintf("  en-us.yaml is missing the display-name key %q", "locale."+code))
		}
	}

	sort.Strings(problems)

	fmt.Fprintln(w, "Registration checks:")
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(w, p)
		}
		return findingsError("registration checks failed")
	}
	fmt.Fprintln(w, "Registration checks passed.")
	return nil
}

// parseTypeUnion returns the quoted string members of the TypeScript
// declaration "export type <name> = 'a' | 'b' ...;" in the file at path.
// An unreadable file or a missing declaration is an operational error.
func parseTypeUnion(path, name string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	declRe := regexp.MustCompile(`export\s+type\s+` + regexp.QuoteMeta(name) + `\s*=([^;]*);`)
	decl := declRe.FindSubmatch(data)
	if decl == nil {
		return nil, fmt.Errorf("%s: no \"export type %s\" declaration", filepath.Base(path), name)
	}
	members := make(map[string]bool)
	for _, m := range unionMemberRe.FindAllSubmatch(decl[1], -1) {
		members[string(m[1])] = true
	}
	return members, nil
}

// unionMemberRe matches one quoted string member of a TypeScript union.
var unionMemberRe = regexp.MustCompile(`'([^']*)'`)

// reportCheckLocale runs the per-locale checks:
//   - locale file present
//   - no stale keys
//   - validate passes (placeholders, ICU structure, tags, metadata,
//     deliberate identity)
//
// With strict, the locale must also be complete:
//   - no missing keys
//   - no drifted keys
func reportCheckLocale(w io.Writer, root, locale string, strict bool) error {
	passed := true
	printResult := func(label string, ok bool, detail string) {
		status := "OK"
		if !ok {
			status = "FAIL"
			passed = false
		}
		if detail != "" {
			fmt.Fprintf(w, "  %-35s %s  %s\n", label+":", status, detail)
		} else {
			fmt.Fprintf(w, "  %-35s %s\n", label+":", status)
		}
	}

	label := "Checks"
	if strict {
		label = "Strict checks"
	}
	fmt.Fprintf(w, "%s for %s:\n", label, locale)

	localePath := translationsPath(root, locale+".yaml")
	enPath := translationsPath(root, "en-us.yaml")

	enKeys, err := loadYAMLFlat(enPath)
	if err != nil {
		return err
	}

	localeKeys, localeErr := loadYAMLFlat(localePath)
	printResult("locale file readable", localeErr == nil, errString(localeErr))
	if localeErr != nil {
		localeKeys = make(map[string]string)
	}

	// No stale keys.
	staleCount := len(computeStale(enKeys, localeKeys))
	printResult("no stale keys", staleCount == 0, countDetail(staleCount))

	// Validate passes.
	validateErr := reportValidateQuiet(root, locale)
	printResult("validate passes", validateErr == nil, errString(validateErr))

	// Load @source snapshots for the drift check below. Their coherence is
	// already covered by the "validate passes" check above (validateLocale
	// checks it).
	meta, metaErr := loadSources(root, locale)
	if metaErr != nil {
		printResult("@source readable", false, errString(metaErr))
	}

	// Completeness checks.
	if strict {
		missingCount := len(computeMissing(enKeys, localeKeys))
		printResult("no missing keys", missingCount == 0, countDetail(missingCount))

		if meta != nil {
			driftCount := len(computeDrifted(enKeys, meta, localeKeys))
			printResult("no drifted keys", driftCount == 0, countDetail(driftCount))
		}
	}

	if passed {
		fmt.Fprintf(w, "All checks passed for %s.\n", locale)
		return nil
	}
	return findingsError(fmt.Sprintf("checks failed for %s", locale))
}

// reportValidateQuiet runs validate and returns an error summary without
// printing individual errors.
func reportValidateQuiet(root, locale string) error {
	errs, err := validateLocale(root, locale)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d validation %s", len(errs), plural(len(errs), "error"))
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func countDetail(count int) string {
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("%d found", count)
}
