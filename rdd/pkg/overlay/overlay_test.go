// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package overlay

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/ext4"
	"gotest.tools/v3/assert"
)

func TestLoadManifestParsesOctalAndDefaults(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "overlay.yaml")
	assert.NilError(t, os.WriteFile(manifest, []byte(`
entries:
  - path: /usr/local/bin/tool
    source: tool
    mode: "0755"
  - path: /etc/keep
    type: dir
`), 0o644))

	m, err := LoadManifest(manifest)
	assert.NilError(t, err)
	assert.Equal(t, len(m.Entries), 2)

	mode, err := m.Entries[0].mode(0o644)
	assert.NilError(t, err)
	assert.Equal(t, mode, os.FileMode(0o755))
	assert.Equal(t, m.Entries[1].kind(), TypeDir)
}

func TestValidateRejectsBadEntries(t *testing.T) {
	cases := map[string]Entry{
		"relative path":   {Path: "usr/local/bin/x", Source: "x"},
		"file no source":  {Path: "/x"},
		"symlink target":  {Path: "/x", Type: TypeSymlink},
		"dir with target": {Path: "/x", Type: TypeDir, Target: "/y"},
		"escaping source": {Path: "/x", Source: "../x"},
		"bad mode":        {Path: "/x", Source: "x", Mode: "9999"},
		"unknown type":    {Path: "/x", Type: "socket"},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Assert(t, e.validate() != nil, "expected validation error")
		})
	}
}

func TestApplyTarOverridesNewFilesDirsAndLinks(t *testing.T) {
	base := buildTar(t, []tarItem{
		{name: "usr/local/bin/", dir: true},
		{name: "usr/local/bin/old", body: "OLD"},
		{name: "etc/keep.conf", body: "KEEP"},
	})

	source := t.TempDir()
	writeSource(t, source, "bin/old", "NEWOLD")
	writeSource(t, source, "bin/new", "NEW")

	m := &Manifest{Entries: []Entry{
		{Path: "/usr/local/bin/old", Source: "bin/old", Mode: "0755", UID: 1, GID: 2},
		{Path: "/usr/local/bin/new", Source: "bin/new", Mode: "0700"},
		{Path: "/etc/foo.d", Type: TypeDir, Mode: "0750", UID: 3, GID: 4},
		{Path: "/etc/link", Type: TypeSymlink, Target: "/usr/local/bin/new"},
	}}

	var out bytes.Buffer
	assert.NilError(t, ApplyTar(bytes.NewReader(base), &out, m, source, testMtime))
	got := readTar(t, out.Bytes())

	override := got["usr/local/bin/old"]
	assert.Equal(t, override.body, "NEWOLD")
	assert.Equal(t, override.hdr.Mode, int64(0o755))
	assert.Equal(t, override.hdr.Uid, 1)
	assert.Equal(t, override.hdr.Gid, 2)

	added := got["usr/local/bin/new"]
	assert.Equal(t, added.body, "NEW")
	assert.Equal(t, added.hdr.Mode, int64(0o700))

	assert.Equal(t, got["etc/keep.conf"].body, "KEEP")

	dir := got["etc/foo.d"]
	assert.Equal(t, dir.hdr.Typeflag, byte(tar.TypeDir))
	assert.Equal(t, dir.hdr.Mode, int64(0o750))
	assert.Equal(t, dir.hdr.Uid, 3)

	link := got["etc/link"]
	assert.Equal(t, link.hdr.Typeflag, byte(tar.TypeSymlink))
	assert.Equal(t, link.hdr.Linkname, "/usr/local/bin/new")

	assert.Equal(t, count(out.Bytes(), "usr/local/bin/old"), 1)
}

// testMtime is the timestamp every overlay entry is stamped with in the tests.
var testMtime = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

func TestApplyTarStampsParameterMtime(t *testing.T) {
	source := t.TempDir()
	writeSource(t, source, "file", "F")
	// A source file's own mtime is ignored; every entry gets the parameter.
	old := time.Date(2009, 8, 7, 6, 5, 4, 0, time.UTC)
	assert.NilError(t, os.Chtimes(filepath.Join(source, "file"), old, old))

	m := &Manifest{Entries: []Entry{
		{Path: "/file", Source: "file"},
		{Path: "/dir", Type: TypeDir},
		{Path: "/link", Type: TypeSymlink, Target: "/file"},
	}}

	var out bytes.Buffer
	assert.NilError(t, ApplyTar(bytes.NewReader(buildTar(t, nil)), &out, m, source, testMtime))
	got := readTar(t, out.Bytes())

	for _, name := range []string{"file", "dir", "link"} {
		assert.Equal(t, got[name].hdr.ModTime.Unix(), testMtime.Unix(), "entry %s", name)
	}
}

func TestApplyTarCopiesTree(t *testing.T) {
	base := buildTar(t, []tarItem{
		{name: "lib/systemd/", dir: true},
		{name: "lib/systemd/stale.service", body: "STALE"}, // replaced by the tree
	})

	source := t.TempDir()
	writeSource(t, source, "units/fresh.service", "FRESH")
	writeSource(t, source, "units/stale.service", "NEW")
	writeSource(t, source, "units/multi-user.target.wants/run.sh", "#!/bin/sh\n")
	assert.NilError(t, os.Chmod(filepath.Join(source, "units/multi-user.target.wants/run.sh"), 0o755))
	assert.NilError(t, os.Symlink("../fresh.service",
		filepath.Join(source, "units/multi-user.target.wants/fresh.service")))

	m := &Manifest{Entries: []Entry{
		{Path: "/lib/systemd", Type: TypeDir, Source: "units", UID: 0, GID: 0},
	}}

	var out bytes.Buffer
	assert.NilError(t, ApplyTar(bytes.NewReader(base), &out, m, source, testMtime))
	got := readTar(t, out.Bytes())

	assert.Equal(t, got["lib/systemd/fresh.service"].body, "FRESH")
	assert.Equal(t, got["lib/systemd/stale.service"].body, "NEW")       // overridden, not duplicated
	assert.Equal(t, count(out.Bytes(), "lib/systemd/stale.service"), 1) // base copy dropped

	// The copied file keeps its source mode (which lacks a Unix exec bit on Windows).
	runSrc, err := os.Lstat(filepath.Join(source, "units/multi-user.target.wants/run.sh"))
	assert.NilError(t, err)
	assert.Equal(t, got["lib/systemd/multi-user.target.wants/run.sh"].hdr.Mode, int64(runSrc.Mode().Perm()))

	dir := got["lib/systemd/multi-user.target.wants"]
	assert.Equal(t, dir.hdr.Typeflag, byte(tar.TypeDir))

	link := got["lib/systemd/multi-user.target.wants/fresh.service"]
	assert.Equal(t, link.hdr.Typeflag, byte(tar.TypeSymlink))
	assert.Equal(t, link.hdr.Linkname, "../fresh.service")
	assert.Equal(t, link.hdr.ModTime.Unix(), testMtime.Unix())
}

// TestApplyTarExplicitOverridesTree checks an explicit file or dir entry overrides
// a colliding tree member regardless of manifest order, with no duplicate entry.
func TestApplyTarExplicitOverridesTree(t *testing.T) {
	source := t.TempDir()
	writeSource(t, source, "tree/a.conf", "TREE-A")
	writeSource(t, source, "tree/sub/x.conf", "X")
	writeSource(t, source, "explicit-a.conf", "EXPLICIT-A")

	tree := Entry{Path: "/etc/app", Type: TypeDir, Source: "tree"}
	file := Entry{Path: "/etc/app/a.conf", Source: "explicit-a.conf", UID: 472, Mode: "0600"}
	dir := Entry{Path: "/etc/app/sub", Type: TypeDir, Mode: "0700", UID: 5}

	for name, entries := range map[string][]Entry{
		"explicit after tree":  {tree, file, dir},
		"explicit before tree": {file, dir, tree},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			assert.NilError(t, ApplyTar(bytes.NewReader(buildTar(t, nil)),
				&out, &Manifest{Entries: entries}, source, testMtime))
			got := readTar(t, out.Bytes())

			a := got["etc/app/a.conf"]
			assert.Equal(t, a.body, "EXPLICIT-A")
			assert.Equal(t, a.hdr.Mode, int64(0o600))
			assert.Equal(t, a.hdr.Uid, 472)
			assert.Equal(t, count(out.Bytes(), "etc/app/a.conf"), 1) // not duplicated

			sub := got["etc/app/sub"]
			assert.Equal(t, sub.hdr.Mode, int64(0o700))
			assert.Equal(t, sub.hdr.Uid, 5)
			assert.Equal(t, got["etc/app/sub/x.conf"].body, "X") // tree child still placed
		})
	}
}

// TestImageExplicitOverridesTree is the ext4 counterpart, where the two backends
// previously disagreed on which order won.
func TestImageExplicitOverridesTree(t *testing.T) {
	source := t.TempDir()
	writeSource(t, source, "tree/a.conf", "TREE-A")
	writeSource(t, source, "tree/sub/x.conf", "X")
	writeSource(t, source, "explicit-a.conf", "EXPLICIT-A")

	tree := Entry{Path: "/etc/app", Type: TypeDir, Source: "tree"}
	file := Entry{Path: "/etc/app/a.conf", Source: "explicit-a.conf", Mode: "0600"}
	dir := Entry{Path: "/etc/app/sub", Type: TypeDir, Mode: "0700"}

	for name, entries := range map[string][]Entry{
		"explicit after tree":  {tree, file, dir},
		"explicit before tree": {file, dir, tree},
	} {
		t.Run(name, func(t *testing.T) {
			img := filepath.Join(t.TempDir(), "distro.raw")
			assert.NilError(t, Apply(newImage(t, img), &Manifest{Entries: entries}, source, testMtime))
			fs := openImageFS(t, img)

			sub, err := fs.Stat("etc/app/sub")
			assert.NilError(t, err)
			assert.Equal(t, sub.Mode().Perm(), os.FileMode(0o700))

			a, err := fs.Stat("etc/app/a.conf")
			assert.NilError(t, err)
			assert.Equal(t, a.Mode().Perm(), os.FileMode(0o600))
		})
	}
}

// TestImageStampsParameterMtime checks the ext4 backend stamps the parameter
// mtime on a file, on every ancestor directory it creates, and on a symlink.
func TestImageStampsParameterMtime(t *testing.T) {
	source := t.TempDir()
	writeSource(t, source, "tool", "BIN")

	m := &Manifest{Entries: []Entry{
		{Path: "/usr/local/bin/tool", Source: "tool"}, // creates the usr/local/bin chain
		{Path: "/usr/local/bin/link", Type: TypeSymlink, Target: "tool"},
	}}

	img := filepath.Join(t.TempDir(), "distro.raw")
	assert.NilError(t, Apply(newImage(t, img), m, source, testMtime))

	fs := openImageFS(t, img)
	for _, p := range []string{"usr", "usr/local", "usr/local/bin", "usr/local/bin/tool", "usr/local/bin/link"} {
		info, err := fs.Stat(p)
		assert.NilError(t, err)
		assert.Equal(t, info.ModTime().Unix(), testMtime.Unix(), "mtime of %s", p)
	}
}

// newImage creates an empty ext4 image and returns an imageDistro writing into it.
func newImage(t *testing.T, path string) *imageDistro {
	t.Helper()
	d, err := diskfs.Create(path, 64*1024*1024, diskfs.SectorSizeDefault)
	assert.NilError(t, err)
	fsi, err := d.CreateFilesystem(disk.FilesystemSpec{Partition: 0, FSType: filesystem.TypeExt4})
	assert.NilError(t, err)
	return &imageDistro{disk: d, fs: fsi.(*ext4.FileSystem)}
}

// openImageFS reopens an ext4 image read-only for inspection. The disk handle is
// closed at test end so Windows can delete the image in TempDir cleanup.
func openImageFS(t *testing.T, path string) *ext4.FileSystem {
	t.Helper()
	d, err := diskfs.Open(path, diskfs.WithOpenMode(diskfs.ReadOnly))
	assert.NilError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	fsi, err := d.GetFilesystem(0)
	assert.NilError(t, err)
	return fsi.(*ext4.FileSystem)
}

type tarItem struct {
	name string
	body string
	dir  bool
}

func buildTar(t *testing.T, items []tarItem) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, it := range items {
		hdr := &tar.Header{Name: it.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(it.body))}
		if it.dir {
			hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeDir, 0o755, 0
		}
		assert.NilError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(it.body))
		assert.NilError(t, err)
	}
	assert.NilError(t, tw.Close())
	return buf.Bytes()
}

type tarEntry struct {
	hdr  tar.Header
	body string
}

func readTar(t *testing.T, data []byte) map[string]tarEntry {
	t.Helper()
	out := map[string]tarEntry{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		assert.NilError(t, err)
		body, err := io.ReadAll(tr)
		assert.NilError(t, err)
		name := hdr.Name
		if hdr.Typeflag == tar.TypeDir {
			name = name[:len(name)-1] // drop trailing slash for lookup
		}
		out[name] = tarEntry{hdr: *hdr, body: string(body)}
	}
	return out
}

func writeSource(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	assert.NilError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	assert.NilError(t, os.WriteFile(full, []byte(body), 0o644))
}

func count(data []byte, name string) int {
	n := 0
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Name == name {
			n++
		}
	}
	return n
}
