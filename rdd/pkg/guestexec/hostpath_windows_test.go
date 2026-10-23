// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package guestexec

import (
	"os"
	"testing"

	"gotest.tools/v3/assert"
)

func TestTranslateHostPathResolvesAgainstProcessCwd(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	assert.NilError(t, err)
	want, err := TranslateHostPath(cwd + `\sub`)
	assert.NilError(t, err)
	// cwd[:2] is the current drive, e.g. "C:".
	for _, arg := range []string{`sub`, cwd[:2] + `sub`} {
		got, err := TranslateHostPath(arg)
		assert.NilError(t, err)
		assert.Equal(t, got, want, "arg %q", arg)
	}
}
