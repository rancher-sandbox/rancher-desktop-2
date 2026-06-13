// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package overlay layers Rancher Desktop assets onto a pristine openSUSE distro
// at build time, writing into both the WSL tarball and the Lima ext4 image from
// a single manifest. Ownership and permissions come from the manifest, so the
// build needs no root and the host's own uids never reach the distro.
package overlay

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"

	"sigs.k8s.io/yaml"
)

// Entry types.
const (
	TypeFile    = "file"
	TypeDir     = "dir"
	TypeSymlink = "symlink"
)

// Manifest describes the assets to merge into a distro.
type Manifest struct {
	Entries []Entry `json:"entries"`
}

// Entry is a single file, directory, or symlink to place in the distro.
type Entry struct {
	// Path is the absolute destination inside the distro root.
	Path string `json:"path"`
	// Type is file (the default), dir, or symlink.
	Type string `json:"type,omitempty"`
	// Source names the contents as a path relative to the source directory: a
	// file for a file entry, or a directory tree to copy for a dir entry.
	Source string `json:"source,omitempty"`
	// Target is the symlink target, stored verbatim.
	Target string `json:"target,omitempty"`
	// UID and GID own the entry; both default to 0 (root).
	UID int `json:"uid,omitempty"`
	GID int `json:"gid,omitempty"`
	// Mode is an octal string such as "0755"; it defaults to 0644 for files and
	// 0755 for directories.
	Mode string `json:"mode,omitempty"`
}

// LoadManifest reads and validates a YAML manifest.
func LoadManifest(p string) (*Manifest, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.UnmarshalStrict(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", p, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest %s: %w", p, err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	for i := range m.Entries {
		if err := m.Entries[i].validate(); err != nil {
			return fmt.Errorf("entry %d (%s): %w", i, m.Entries[i].Path, err)
		}
	}
	return nil
}

// kind returns the entry type, defaulting to TypeFile.
func (e *Entry) kind() string {
	if e.Type == "" {
		return TypeFile
	}
	return e.Type
}

func (e *Entry) validate() error {
	if !path.IsAbs(e.Path) || e.Path != path.Clean(e.Path) {
		return errors.New("path must be absolute and clean")
	}
	switch e.kind() {
	case TypeFile:
		if e.Source == "" {
			return errors.New("file entry needs a source")
		}
		if e.Target != "" {
			return errors.New("file entry cannot have a target")
		}
	case TypeDir:
		if e.Target != "" {
			return errors.New("dir entry cannot have a target")
		}
	case TypeSymlink:
		if e.Target == "" {
			return errors.New("symlink entry needs a target")
		}
		if e.Source != "" {
			return errors.New("symlink entry cannot have a source")
		}
	default:
		return fmt.Errorf("unknown type %q (want file, dir, or symlink)", e.Type)
	}
	if e.Source != "" && !fs.ValidPath(e.Source) {
		return errors.New("source must be relative and within the source directory")
	}
	if e.Mode != "" {
		if _, err := e.mode(0); err != nil {
			return fmt.Errorf("invalid mode %q: %w", e.Mode, err)
		}
	}
	return nil
}

// mode parses the octal Mode, returning def when it is unset.
func (e *Entry) mode(def os.FileMode) (os.FileMode, error) {
	if e.Mode == "" {
		return def, nil
	}
	v, err := strconv.ParseUint(e.Mode, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(v), nil
}
