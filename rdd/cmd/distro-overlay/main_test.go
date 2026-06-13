// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestParseMtime(t *testing.T) {
	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

	t.Run("epoch seconds", func(t *testing.T) {
		got, err := parseMtime("1577934245")
		assert.NilError(t, err)
		assert.Equal(t, got.Unix(), want.Unix())
	})

	t.Run("RFC3339", func(t *testing.T) {
		got, err := parseMtime("2020-01-02T03:04:05Z")
		assert.NilError(t, err)
		assert.Equal(t, got.Unix(), want.Unix())
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := parseMtime("last tuesday")
		assert.Assert(t, err != nil)
	})
}
