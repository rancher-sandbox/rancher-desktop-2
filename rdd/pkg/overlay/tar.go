// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package overlay

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

// tarDistro appends overlay entries to an output tar stream.
type tarDistro struct {
	tw       *tar.Writer
	baseDirs map[string]bool // directories already present in the base tarball
	emitted  map[string]bool // directories this backend has written
}

// ApplyTar copies the base tarball to out, dropping any path the manifest
// overrides, then appends the overlay entries stamped with mtime.
func ApplyTar(base io.Reader, out io.Writer, m *Manifest, sourceDir string, mtime time.Time) error {
	override, err := overridePaths(m, sourceDir)
	if err != nil {
		return err
	}
	tr := tar.NewReader(base)
	tw := tar.NewWriter(out)
	baseDirs := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading base tarball: %w", err)
		}
		name := tarClean(hdr.Name)
		if hdr.Typeflag == tar.TypeDir {
			baseDirs[name] = true
		}
		if override[name] {
			continue // the overlay replaces this path
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
	d := &tarDistro{tw: tw, baseDirs: baseDirs, emitted: map[string]bool{}}
	return Apply(d, m, sourceDir, mtime)
}

// overridePaths returns every destination the manifest writes, so ApplyTar can
// drop the matching base entries. A dir entry that copies a source tree expands
// to all of its members.
func overridePaths(m *Manifest, sourceDir string) (map[string]bool, error) {
	set := make(map[string]bool, len(m.Entries))
	for i := range m.Entries {
		e := &m.Entries[i]
		set[tarClean(e.Path)] = true
		if e.kind() == TypeDir && e.Source != "" {
			members, err := walkTree(sourceDir, e)
			if err != nil {
				return nil, err
			}
			for _, mem := range members {
				set[tarClean(mem.dest)] = true
			}
		}
	}
	return set, nil
}

// tarClean turns a path into the relative, slash-separated form tar uses.
func tarClean(p string) string {
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

func (t *tarDistro) EnsureDir(dir string, uid, gid int, mode os.FileMode, mtime time.Time, force bool) error {
	name := tarClean(dir)
	if name == "" {
		return nil
	}
	// Emit missing ancestors first, with default ownership, so the tar carries a
	// full directory chain rather than relying on the extractor's umask.
	if parent := path.Dir(name); parent != "." {
		if err := t.EnsureDir("/"+parent, 0, 0, 0o755, mtime, false); err != nil {
			return err
		}
	}
	if t.emitted[name] || (t.baseDirs[name] && !force) {
		return nil
	}
	t.emitted[name] = true
	return t.tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir,
		Name:     name + "/",
		Mode:     int64(mode.Perm()),
		Uid:      uid,
		Gid:      gid,
		ModTime:  mtime,
	})
}

func (t *tarDistro) WriteFile(file string, contents io.Reader, uid, gid int, mode os.FileMode, mtime time.Time) error {
	buf, err := io.ReadAll(contents)
	if err != nil {
		return err
	}
	if err := t.tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     tarClean(file),
		Mode:     int64(mode.Perm()),
		Uid:      uid,
		Gid:      gid,
		Size:     int64(len(buf)),
		ModTime:  mtime,
	}); err != nil {
		return err
	}
	_, err = t.tw.Write(buf)
	return err
}

func (t *tarDistro) Symlink(link, target string, uid, gid int, mtime time.Time) error {
	return t.tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink,
		Name:     tarClean(link),
		Linkname: target,
		Mode:     0o777,
		Uid:      uid,
		Gid:      gid,
		ModTime:  mtime,
	})
}

func (t *tarDistro) Close() error {
	return t.tw.Close()
}
