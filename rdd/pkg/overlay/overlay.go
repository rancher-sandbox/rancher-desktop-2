// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package overlay

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

// Distro is a mutable distro root. The tarball and ext4 image backends both
// implement it, so Apply drives them identically.
type Distro interface {
	// EnsureDir creates dir and any missing parents. When force is true it applies
	// uid/gid/mode/mtime even to a directory that already exists; otherwise it
	// leaves an existing directory untouched.
	EnsureDir(dir string, uid, gid int, mode os.FileMode, mtime time.Time, force bool) error
	// WriteFile creates or replaces a regular file with the given contents.
	WriteFile(file string, contents io.Reader, uid, gid int, mode os.FileMode, mtime time.Time) error
	// Symlink creates a symbolic link pointing to target.
	Symlink(link, target string, uid, gid int, mtime time.Time) error
	// Close flushes pending changes.
	Close() error
}

// Apply writes every manifest entry into the distro, stamping mtime on each, and
// closes it. An explicit entry always overrides a path that a tree copy would also
// write, whatever the manifest order: directories are created first with their
// owner and mode resolved up front, then files and symlinks are written.
func Apply(d Distro, m *Manifest, sourceDir string, mtime time.Time) error {
	owned := entryPaths(m)
	dirs, err := plannedDirs(m, sourceDir)
	if err != nil {
		return err
	}
	// Create directories parents-first so each is written once with its final
	// owner and mode, before any file or symlink creates the same path implicitly.
	for _, dir := range sortedKeys(dirs) {
		meta := dirs[dir]
		if err := d.EnsureDir(dir, meta.uid, meta.gid, meta.mode, mtime, true); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}
	for i := range m.Entries {
		e := &m.Entries[i]
		switch {
		case e.kind() == TypeDir && e.Source == "":
			continue // created above
		case e.kind() == TypeDir:
			if err := writeTreeFiles(d, e, sourceDir, mtime, owned); err != nil {
				return err
			}
		case e.kind() == TypeSymlink:
			if err := writeParent(d, e.Path, mtime); err != nil {
				return err
			}
			if err := d.Symlink(e.Path, e.Target, e.UID, e.GID, mtime); err != nil {
				return fmt.Errorf("creating symlink %s: %w", e.Path, err)
			}
		default: // TypeFile
			if err := writeParent(d, e.Path, mtime); err != nil {
				return err
			}
			if err := writeFile(d, e.Path, filepath.Join(sourceDir, filepath.FromSlash(e.Source)),
				e.UID, e.GID, modeOr(e, 0o644), mtime); err != nil {
				return err
			}
		}
	}
	return d.Close()
}

// dirMeta is the resolved owner and mode for a directory the overlay creates.
type dirMeta struct {
	uid, gid int
	mode     os.FileMode
}

// plannedDirs resolves every directory the overlay creates to its final owner and
// mode. Tree roots and their subdirectories come first; an explicit dir entry then
// overrides any path it shares with a tree, so explicit ownership always wins.
func plannedDirs(m *Manifest, sourceDir string) (map[string]dirMeta, error) {
	dirs := map[string]dirMeta{}
	for i := range m.Entries {
		e := &m.Entries[i]
		if e.kind() != TypeDir || e.Source == "" {
			continue
		}
		dirs[e.Path] = dirMeta{e.UID, e.GID, modeOr(e, 0o755)}
		members, err := walkTree(sourceDir, e)
		if err != nil {
			return nil, err
		}
		for _, mem := range members {
			if mem.info.IsDir() {
				dirs[mem.dest] = dirMeta{e.UID, e.GID, mem.info.Mode().Perm()}
			}
		}
	}
	for i := range m.Entries {
		e := &m.Entries[i]
		if e.kind() == TypeDir && e.Source == "" {
			dirs[e.Path] = dirMeta{e.UID, e.GID, modeOr(e, 0o755)}
		}
	}
	return dirs, nil
}

// writeTreeFiles writes the files and symlinks under a tree copy, skipping any path
// an explicit entry owns. The tree's directories are created earlier by plannedDirs.
func writeTreeFiles(d Distro, e *Entry, sourceDir string, mtime time.Time, owned map[string]bool) error {
	members, err := walkTree(sourceDir, e)
	if err != nil {
		return err
	}
	for _, mem := range members {
		if mem.info.IsDir() || owned[mem.dest] {
			continue
		}
		switch {
		case mem.info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(mem.srcPath)
			if err != nil {
				return err
			}
			// The Linux distro needs a slash-separated target even when the tool
			// runs on a Windows build host, where Readlink returns backslashes.
			if err := d.Symlink(mem.dest, filepath.ToSlash(target), e.UID, e.GID, mtime); err != nil {
				return fmt.Errorf("creating symlink %s: %w", mem.dest, err)
			}
		case mem.info.Mode().IsRegular():
			if err := writeFile(d, mem.dest, mem.srcPath, e.UID, e.GID, mem.info.Mode().Perm(), mtime); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: unsupported file type %s", mem.srcPath, mem.info.Mode().Type())
		}
	}
	return nil
}

// entryPaths is the set of destinations the manifest writes explicitly, so a tree
// copy can yield a colliding member to the entry that names it.
func entryPaths(m *Manifest) map[string]bool {
	set := make(map[string]bool, len(m.Entries))
	for i := range m.Entries {
		set[m.Entries[i].Path] = true
	}
	return set
}

// sortedKeys returns the directory paths sorted so each ancestor precedes its
// descendants, which lexical order guarantees (a path sorts before its extensions).
func sortedKeys(dirs map[string]dirMeta) []string {
	keys := make([]string, 0, len(dirs))
	for k := range dirs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeParent creates the parent directory of dest with default ownership, leaving
// a directory another entry already established untouched.
func writeParent(d Distro, dest string, mtime time.Time) error {
	if err := d.EnsureDir(path.Dir(dest), 0, 0, 0o755, mtime, false); err != nil {
		return fmt.Errorf("creating parent of %s: %w", dest, err)
	}
	return nil
}

// treeMember is one file, directory, or symlink found under a copied source tree.
type treeMember struct {
	dest    string      // absolute destination path inside the distro
	srcPath string      // absolute path of the source on the build host
	info    fs.FileInfo // lstat of the source (type, permissions)
}

// walkTree lists every member under a dir entry's source tree, excluding the
// tree root itself. Members are ordered parents before children.
func walkTree(sourceDir string, e *Entry) ([]treeMember, error) {
	root := filepath.Join(sourceDir, filepath.FromSlash(e.Source))
	var members []treeMember
	err := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // the root is created by the caller
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		members = append(members, treeMember{
			dest:    path.Join(e.Path, filepath.ToSlash(rel)),
			srcPath: p,
			info:    info,
		})
		return nil
	})
	return members, err
}

// modeOr returns the entry's mode, or def when it is unset. The mode string is
// validated when the manifest loads, so parsing cannot fail here.
func modeOr(e *Entry, def os.FileMode) os.FileMode {
	mode, _ := e.mode(def)
	return mode
}

func writeFile(d Distro, dest, srcPath string, uid, gid int, mode os.FileMode, mtime time.Time) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("opening source for %s: %w", dest, err)
	}
	defer src.Close()
	if err := d.WriteFile(dest, src, uid, gid, mode, mtime); err != nil {
		return fmt.Errorf("writing file %s: %w", dest, err)
	}
	return nil
}
