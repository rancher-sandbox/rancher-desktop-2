// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package guestdeps reads the guest dependency manifest and stages the assets
// it lists for a build: it picks the asset matching the build target, fetches
// it, and verifies the bytes against the checksum the manifest records.
//
// The manifest is written by `yarn rddepman guest`, which resolves every
// download URL and checksum ahead of time, so nothing here needs per-package
// knowledge.
package guestdeps

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// checksumPattern is the form `yarn rddepman` writes checksums in.
var checksumPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// An Asset is one downloadable artifact of a dependency.
type Asset struct {
	// Platform is the Go platform name the artifact targets, plus `wsl` for a
	// WSL-specific build.
	Platform string `json:"platform"`
	// Arch is the Go architecture name. It is empty for arch-independent
	// artifacts, which match any architecture.
	Arch string `json:"arch,omitempty"`
	// Variant distinguishes artifacts that share a platform and architecture
	// but differ in kind, such as the distro's raw disk image and its rootfs
	// tarball.
	Variant string `json:"variant,omitempty"`
	// URL is the fully-resolved download URL.
	URL string `json:"url"`
	// Checksum is the sha256 of the downloaded bytes, `sha256:`-prefixed.
	Checksum string `json:"checksum"`
}

// An Entry is one dependency in the manifest.
type Entry struct {
	Version string  `json:"version"`
	Assets  []Asset `json:"assets"`
}

// A Manifest holds the guest dependencies, keyed by dependency name.
type Manifest map[string]Entry

// LoadManifest reads and validates the manifest at manifestPath.
func LoadManifest(manifestPath string) (Manifest, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", manifestPath, err)
	}
	for name, entry := range m {
		// cachePath joins the name into the download's path, alongside the version
		// and file name that validate checks, so the name needs the same guard.
		if !isPathElement(name) {
			return nil, fmt.Errorf("dependency %q in %s: the name is not a single directory name", name, manifestPath)
		}
		if err := entry.validate(); err != nil {
			return nil, fmt.Errorf("dependency %s in %s: %w", name, manifestPath, err)
		}
	}
	return m, nil
}

// validate rejects an entry a downloader cannot act on. Everything it checks is
// something `yarn rddepman` writes, so a failure means the manifest was
// hand-edited or the two sides have drifted apart.
func (e Entry) validate() error {
	if e.Version == "" {
		return errors.New("has no version")
	}
	if !isPathElement(e.Version) {
		return fmt.Errorf("has version %q, want a single directory name", e.Version)
	}
	for _, asset := range e.Assets {
		if err := asset.validate(); err != nil {
			return err
		}
	}
	return nil
}

// isPathElement reports whether name is usable as one directory or file name of
// a cached download. cachePath builds that path with filepath.Join, which
// treats a backslash as a separator on Windows, and url.Parse decodes %5C into
// one, so checking only for a slash would leave a traversal open there.
func isPathElement(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

func (a Asset) validate() error {
	u, err := url.Parse(a.URL)
	if err != nil {
		return fmt.Errorf("asset %s has an unparseable url %q: %w", a, a.URL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("asset %s has url %q, want https", a, a.URL)
	}
	// url.Parse accepts https:///name, which only fails once the request is
	// made, several retries into a build.
	if u.Hostname() == "" {
		return fmt.Errorf("asset %s has url %q with no host", a, a.URL)
	}
	// A path ending in a slash names a directory, and path.Base would hand its
	// name to the download as a file name.
	if !isPathElement(path.Base(u.Path)) || strings.HasSuffix(u.Path, "/") {
		return fmt.Errorf("asset %s has url %q with no file name to download to", a, a.URL)
	}
	if !checksumPattern.MatchString(a.Checksum) {
		return fmt.Errorf("asset %s has checksum %q, want sha256:<64 lowercase hex digits>", a, a.Checksum)
	}
	return nil
}

// String renders an asset by the axes it is selected on. It leaves out the url,
// so an error about the url has to print that itself.
func (a Asset) String() string {
	parts := []string{a.Platform}
	if a.Arch != "" {
		parts = append(parts, a.Arch)
	}
	if a.Variant != "" {
		parts = append(parts, a.Variant)
	}
	return "[" + strings.Join(parts, "/") + "]"
}

// A Selector picks one of a dependency's assets. Platform and Arch name the
// build target; Variant is set only for a dependency that ships several assets
// for one target.
type Selector struct {
	Platform string
	Arch     string
	Variant  string
}

// matches reports whether asset satisfies the selector. An empty Arch or
// Variant matches any value, as does an asset with no architecture, which is
// arch-independent. The platform always has to match.
func (s Selector) matches(a Asset) bool {
	return a.Platform == s.Platform &&
		(s.Arch == "" || a.Arch == "" || a.Arch == s.Arch) &&
		(s.Variant == "" || a.Variant == s.Variant)
}

// String renders a selector for error messages.
func (s Selector) String() string {
	parts := []string{"platform=" + s.Platform}
	if s.Arch != "" {
		parts = append(parts, "arch="+s.Arch)
	}
	if s.Variant != "" {
		parts = append(parts, "variant="+s.Variant)
	}
	return strings.Join(parts, ", ")
}

// A Dependency is the asset of one manifest entry chosen for a build target,
// with the name and version it belongs to.
type Dependency struct {
	Name    string
	Version string
	Asset   Asset
}

// Select returns the asset of the named dependency that matches sel. Exactly
// one must match; a manifest that grows a second matching asset fails the
// build instead of letting the list order decide.
func (m Manifest) Select(name string, sel Selector) (Dependency, error) {
	entry, ok := m[name]
	if !ok {
		return Dependency{}, fmt.Errorf("no dependency %q in the manifest", name)
	}
	var matches []Asset
	for _, asset := range entry.Assets {
		if sel.matches(asset) {
			matches = append(matches, asset)
		}
	}
	if len(matches) != 1 {
		return Dependency{}, fmt.Errorf("want exactly one %s asset for %s, found %d: %v",
			name, sel, len(matches), matches)
	}
	return Dependency{Name: name, Version: entry.Version, Asset: matches[0]}, nil
}
