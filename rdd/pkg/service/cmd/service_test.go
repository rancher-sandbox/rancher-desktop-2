// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package service

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestInstanceDistroNames(t *testing.T) {
	t.Run("maps instance directories to distro names and skips reserved dirs and files", func(t *testing.T) {
		limaHome := t.TempDir()
		for _, name := range []string{"rd", "other"} {
			assert.NilError(t, os.Mkdir(filepath.Join(limaHome, name), 0o755))
		}
		// Lima's reserved bookkeeping directories (e.g. _config, _disks) are
		// not instances and must be skipped.
		assert.NilError(t, os.Mkdir(filepath.Join(limaHome, "_config"), 0o755))
		// A stray file must not be treated as an instance either.
		assert.NilError(t, os.WriteFile(filepath.Join(limaHome, "stray"), nil, 0o644))

		// os.ReadDir returns entries sorted by name; "_config" and "stray" drop out.
		assert.DeepEqual(t, instanceDistroNames(limaHome), []string{"lima-other", "lima-rd"})
	})

	t.Run("returns nil when the Lima home does not exist", func(t *testing.T) {
		assert.Assert(t, instanceDistroNames(filepath.Join(t.TempDir(), "missing")) == nil)
	})
}
