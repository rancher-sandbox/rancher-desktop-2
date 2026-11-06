// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

func TestComputeWindowsPath(t *testing.T) {
	cases := []struct {
		name           string
		current        string
		binDir         string
		strategy       appv1alpha1.AddPathStrategy
		removeExisting bool
		want           string
		wantChanged    bool
	}{
		{
			name:        "front prepends",
			current:     `C:\Windows;C:\Windows\System32`,
			binDir:      `C:\Users\me\.rd2\bin`,
			strategy:    appv1alpha1.AddPathFront,
			want:        `C:\Users\me\.rd2\bin;C:\Windows;C:\Windows\System32`,
			wantChanged: true,
		},
		{
			name:        "back appends",
			current:     `C:\Windows`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathBack,
			want:        `C:\Windows;C:\bin`,
			wantChanged: true,
		},
		{
			name:           "manual with marker removes our entry at the front",
			current:        `C:\bin;C:\Windows`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `C:\Windows`,
			wantChanged:    true,
		},
		{
			name:        "manual without marker leaves a front entry alone",
			current:     `C:\bin;C:\Windows`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathManual,
			want:        `C:\bin;C:\Windows`,
			wantChanged: false,
		},
		{
			name:        "front is idempotent when already at front",
			current:     `C:\bin;C:\Windows`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathFront,
			want:        `C:\bin;C:\Windows`,
			wantChanged: false,
		},
		{
			name:        "moves entry from front to back",
			current:     `C:\bin;C:\Windows`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathBack,
			want:        `C:\Windows;C:\bin`,
			wantChanged: true,
		},
		{
			name:        "dedup is case insensitive",
			current:     `c:\BIN;C:\Windows`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathFront,
			want:        `C:\bin;C:\Windows`,
			wantChanged: true,
		},
		{
			name:        "front on empty path",
			current:     "",
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathFront,
			want:        `C:\bin`,
			wantChanged: true,
		},
		{
			name:        "manual on empty path is a no-op",
			current:     "",
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathManual,
			want:        "",
			wantChanged: false,
		},
		{
			name:        "dedup ignores a trailing backslash",
			current:     `C:\bin\;C:\Windows`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathFront,
			want:        `C:\bin;C:\Windows`,
			wantChanged: true,
		},
		{
			name:        "dedup ignores slash direction",
			current:     `C:/bin;C:\Windows`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathFront,
			want:        `C:\bin;C:\Windows`,
			wantChanged: true,
		},
		{
			name:        "manual leaves a trailing semicolon alone",
			current:     `C:\Windows;C:\Windows\System32;`,
			binDir:      `C:\Users\me\.rd2\bin`,
			strategy:    appv1alpha1.AddPathManual,
			want:        `C:\Windows;C:\Windows\System32;`,
			wantChanged: false,
		},
		{
			name:        "manual leaves an empty segment alone",
			current:     `C:\Windows;;C:\Windows\System32`,
			binDir:      `C:\Users\me\.rd2\bin`,
			strategy:    appv1alpha1.AddPathManual,
			want:        `C:\Windows;;C:\Windows\System32`,
			wantChanged: false,
		},
		{
			name:           "manual with marker removes our entry, keeping empty segments",
			current:        `C:\Windows;C:\bin;`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `C:\Windows;`,
			wantChanged:    true,
		},
		{
			name:        "manual without marker keeps our entry in the middle",
			current:     `C:\Windows;C:\bin;C:\Windows\System32`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathManual,
			want:        `C:\Windows;C:\bin;C:\Windows\System32`,
			wantChanged: false,
		},
		{
			name:           "marker remove takes our entry out of the middle",
			current:        `C:\Windows;C:\bin;C:\Windows\System32`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `C:\Windows;C:\Windows\System32`,
			wantChanged:    true,
		},
		{
			name:           "marker remove strips every copy",
			current:        `C:\bin;C:\Windows;c:/BIN;C:\Windows\System32;C:\bin`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `C:\Windows;C:\Windows\System32`,
			wantChanged:    true,
		},
		{
			name:           "marker remove on a path without our entry is a no-op",
			current:        `C:\Windows;C:\Windows\System32`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `C:\Windows;C:\Windows\System32`,
			wantChanged:    false,
		},
		{
			name:           "manual with marker removes our entry at the back",
			current:        `C:\Windows;C:\bin`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `C:\Windows`,
			wantChanged:    true,
		},
		{
			name:        "manual without marker leaves a back entry alone",
			current:     `C:\Windows;C:\bin`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathManual,
			want:        `C:\Windows;C:\bin`,
			wantChanged: false,
		},
		{
			name:           "manual with marker removes a front entry padded by empty segments",
			current:        `;C:\bin;C:\Windows`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `;C:\Windows`,
			wantChanged:    true,
		},
		{
			name:        "manual without marker keeps a middle entry even with slash and case differences",
			current:     `C:\Windows;c:/BIN;C:\Windows\System32`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathManual,
			want:        `C:\Windows;c:/BIN;C:\Windows\System32`,
			wantChanged: false,
		},
		{
			name:           "manual with marker collapses a duplicated front entry in one pass",
			current:        `C:\bin;C:\bin;C:\Windows`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `C:\Windows`,
			wantChanged:    true,
		},
		{
			name:           "manual with marker keeps unrelated duplicates and removes our entries",
			current:        `C:\foo;c:\foo;C:\bin`,
			binDir:         `C:\bin`,
			strategy:       appv1alpha1.AddPathManual,
			removeExisting: true,
			want:           `C:\foo;c:\foo`,
			wantChanged:    true,
		},
		{
			name:        "manual without marker keeps a duplicated middle entry",
			current:     `C:\Windows;C:\bin;C:\bin;C:\Windows\System32`,
			binDir:      `C:\bin`,
			strategy:    appv1alpha1.AddPathManual,
			want:        `C:\Windows;C:\bin;C:\bin;C:\Windows\System32`,
			wantChanged: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := computeWindowsPath(tc.current, tc.binDir, tc.strategy, tc.removeExisting)
			assert.Equal(t, changed, tc.wantChanged)
			assert.Equal(t, got, tc.want)
			// One pass must be a fixed point: re-applying to our own output
			// changes nothing.
			again, changedAgain := computeWindowsPath(got, tc.binDir, tc.strategy, tc.removeExisting)
			assert.Assert(t, !changedAgain, "second pass changed %q -> %q", got, again)
			assert.Equal(t, again, got)
		})
	}
}
