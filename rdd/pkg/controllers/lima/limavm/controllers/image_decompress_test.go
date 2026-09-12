// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	ulikunitz "github.com/ulikunitz/xz"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/embedded"
)

func TestEnsureImageDecompressedEmbedded(t *testing.T) {
	newInstance := func(t *testing.T) *limatype.Instance {
		t.Helper()
		arch := limatype.X8664
		return &limatype.Instance{
			Dir:    t.TempDir(),
			VMType: limatype.VZ,
			Config: &limatype.LimaYAML{
				Arch: &arch,
				Images: []limatype.Image{{
					File: limatype.File{Location: "embedded:///distro.raw.xz", Arch: arch},
				}},
			},
		}
	}

	t.Run("extracts the embedded image", func(t *testing.T) {
		setEmbeddedDistro(t, xzCompress(t, "embedded image"))
		inst := newInstance(t)

		assert.NilError(t, ensureImageDecompressed(t.Context(), inst))

		got, err := os.ReadFile(filepath.Join(inst.Dir, filenames.Image))
		assert.NilError(t, err)
		assert.Equal(t, string(got), "embedded image")
	})

	t.Run("fails without an embedded image", func(t *testing.T) {
		setEmbeddedDistro(t, "")
		inst := newInstance(t)

		err := ensureImageDecompressed(t.Context(), inst)
		assert.ErrorContains(t, err, "no embedded distro image")
	})
}

// setEmbeddedDistro stands in for the image a with_distro build embeds.
func setEmbeddedDistro(t *testing.T, image string) {
	t.Helper()
	saved := embedded.Distro
	embedded.Distro = image
	t.Cleanup(func() { embedded.Distro = saved })
}

func xzCompress(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := ulikunitz.NewWriter(&buf)
	assert.NilError(t, err)
	_, err = io.WriteString(w, s)
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
	return buf.String()
}

func TestLinkWSL2BaseDisk(t *testing.T) {
	writeImage := func(t *testing.T, dir string) string {
		t.Helper()
		imagePath := filepath.Join(dir, filenames.Image)
		assert.NilError(t, os.WriteFile(imagePath, []byte("decompressed image"), 0o644))
		return imagePath
	}

	t.Run("WSL2 hardlinks basedisk to the image", func(t *testing.T) {
		dir := t.TempDir()
		imagePath := writeImage(t, dir)
		inst := &limatype.Instance{Dir: dir, VMType: limatype.WSL2}

		assert.NilError(t, linkWSL2BaseDisk(inst, imagePath))

		baseDisk := filepath.Join(dir, filenames.BaseDiskLegacy)
		got, err := os.ReadFile(baseDisk)
		assert.NilError(t, err)
		assert.Equal(t, string(got), "decompressed image")
		assert.Assert(t, sameFile(t, imagePath, baseDisk), "basedisk must be a hardlink to image")
	})

	t.Run("non-WSL2 leaves basedisk absent", func(t *testing.T) {
		dir := t.TempDir()
		imagePath := writeImage(t, dir)
		inst := &limatype.Instance{Dir: dir, VMType: limatype.VZ}

		assert.NilError(t, linkWSL2BaseDisk(inst, imagePath))

		_, err := os.Stat(filepath.Join(dir, filenames.BaseDiskLegacy))
		assert.Assert(t, cmp.ErrorIs(err, os.ErrNotExist), "basedisk must not exist for vz")
	})

	t.Run("existing basedisk is left untouched", func(t *testing.T) {
		dir := t.TempDir()
		imagePath := writeImage(t, dir)
		baseDisk := filepath.Join(dir, filenames.BaseDiskLegacy)
		assert.NilError(t, os.WriteFile(baseDisk, []byte("preexisting"), 0o644))
		inst := &limatype.Instance{Dir: dir, VMType: limatype.WSL2}

		assert.NilError(t, linkWSL2BaseDisk(inst, imagePath))

		got, err := os.ReadFile(baseDisk)
		assert.NilError(t, err)
		assert.Equal(t, string(got), "preexisting")
	})

	t.Run("missing image is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		inst := &limatype.Instance{Dir: dir, VMType: limatype.WSL2}

		assert.NilError(t, linkWSL2BaseDisk(inst, filepath.Join(dir, filenames.Image)))

		_, err := os.Stat(filepath.Join(dir, filenames.BaseDiskLegacy))
		assert.Assert(t, cmp.ErrorIs(err, os.ErrNotExist), "basedisk must not exist when image is absent")
	})
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	assert.NilError(t, err)
	bi, err := os.Stat(b)
	assert.NilError(t, err)
	return os.SameFile(ai, bi)
}
