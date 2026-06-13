// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package overlay

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/ext4"
)

// imageDistro writes into the ext4 root partition of an OEM disk image. It
// fills free space the distro reserves at build time; it does not grow the
// filesystem, so an overlay larger than that reserve fails with ENOSPC.
type imageDistro struct {
	disk *disk.Disk
	fs   *ext4.FileSystem
}

// OpenImage opens a raw disk image for in-place modification and locates its
// ext4 root partition.
func OpenImage(p string) (Distro, error) {
	d, err := diskfs.Open(p, diskfs.WithOpenMode(diskfs.ReadWrite))
	if err != nil {
		return nil, err
	}
	table, err := d.GetPartitionTable()
	if err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("reading partition table: %w", err)
	}
	for i := 1; i <= len(table.GetPartitions()); i++ {
		candidate, err := d.GetFilesystem(i)
		if err != nil || candidate.Type() != filesystem.TypeExt4 {
			continue
		}
		if root, ok := candidate.(*ext4.FileSystem); ok {
			return &imageDistro{disk: d, fs: root}, nil
		}
	}
	_ = d.Close()
	return nil, fmt.Errorf("no ext4 root partition in %s", p)
}

// rel converts an absolute distro path to the unrooted form go-diskfs expects.
func rel(p string) string {
	return strings.TrimPrefix(path.Clean(p), "/")
}

func (i *imageDistro) EnsureDir(dir string, uid, gid int, mode os.FileMode, mtime time.Time, force bool) error {
	r := rel(dir)
	if r == "" || r == "." {
		return nil // the root directory always exists
	}
	if info, err := i.fs.Stat(r); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", dir)
		}
		if !force {
			return nil
		}
		return i.setMeta(r, uid, gid, mode, mtime)
	}
	// Create each missing ancestor explicitly so it gets a deterministic mtime;
	// go-diskfs Mkdir would otherwise stamp intermediate dirs with time.Now().
	if parent := path.Dir(r); parent != "." {
		if err := i.EnsureDir("/"+parent, 0, 0, 0o755, mtime, false); err != nil {
			return err
		}
	}
	if err := i.fs.Mkdir(r); err != nil {
		return err
	}
	return i.setMeta(r, uid, gid, mode, mtime)
}

func (i *imageDistro) WriteFile(file string, contents io.Reader, uid, gid int, mode os.FileMode, mtime time.Time) error {
	r := rel(file)
	f, err := i.fs.OpenFile(r, os.O_CREATE|os.O_RDWR|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, contents); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return i.setMeta(r, uid, gid, mode, mtime)
}

func (i *imageDistro) Symlink(link, target string, _, _ int, mtime time.Time) error {
	r := rel(link)
	if err := i.fs.Symlink(target, r); err != nil {
		return err
	}
	// Stamp the link's own mtime for a reproducible image. Chtimes writes the
	// link inode without following it; ownership is left at the go-diskfs default
	// because Chown would follow the link to its target.
	return i.fs.Chtimes(r, mtime, mtime, mtime)
}

func (i *imageDistro) Close() error {
	return i.disk.Close()
}

// setMeta applies ownership, permissions, and the modification time.
func (i *imageDistro) setMeta(r string, uid, gid int, mode os.FileMode, mtime time.Time) error {
	if err := i.fs.Chmod(r, mode); err != nil {
		return err
	}
	if err := i.fs.Chown(r, uid, gid); err != nil {
		return err
	}
	return i.fs.Chtimes(r, mtime, mtime, mtime)
}
