// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package flag_test

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/flag"
)

func TestEnum(t *testing.T) {
	t.Run("accepts valid item", func(t *testing.T) {
		it := flag.NewEnumValue("hello", "world")
		assert.NilError(t, it.Set("world"))
		assert.Equal(t, "world", it.String())
	})
	t.Run("rejects invalid item", func(t *testing.T) {
		it := flag.NewEnumValue("hello", "world")
		assert.ErrorContains(t, it.Set("invalid"), "invalid")
	})
	t.Run("defaults to first option", func(t *testing.T) {
		it := flag.NewEnumValue("hello", "world")
		assert.Equal(t, "hello", it.String())
	})
	t.Run("panics when no options given", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.Assert(t, r != nil, "Expected panic but did not get one")
		}()
		flag.NewEnumValue()
	})
	t.Run("reports type", func(t *testing.T) {
		it := flag.NewEnumValue("hello", "world")
		assert.Equal(t, "enum", it.Type())
	})
}
