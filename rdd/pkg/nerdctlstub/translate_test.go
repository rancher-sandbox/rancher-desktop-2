// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestVolumeArgHandler(t *testing.T) {
	cases := []struct {
		arg  string
		want string
	}{
		{`C:\data:/data`, "/mnt/c/data:/data"},
		{`C:\data:/data:ro`, "/mnt/c/data:/data:ro"},
		{`C:\data:/data:rw`, "/mnt/c/data:/data:rw"},
		{`myvol:/data`, "myvol:/data"},
		{`my-vol_1.0:/data:ro`, "my-vol_1.0:/data:ro"},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			got, cleanups, err := volumeArgHandler(tc.arg)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
			assert.Equal(t, len(cleanups), 0)
		})
	}

	_, _, err := volumeArgHandler(`no-separator`)
	assert.ErrorContains(t, err, "does not contain : separator")
}

func TestMountArgHandler(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		want string
	}{
		{
			name: "bind mount",
			arg:  `type=bind,source=C:\x,target=/y`,
			want: "type=bind,source=/mnt/c/x,target=/y",
		},
		{
			name: "bind mount with src key and bare option",
			arg:  `type=bind,src=C:\x,target=/y,readonly`,
			want: "type=bind,src=/mnt/c/x,target=/y,readonly",
		},
		{
			name: "volume mount stays unchanged",
			arg:  `type=volume,source=vol,target=/y`,
			want: "type=volume,source=vol,target=/y",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := mountArgHandler(tc.arg)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestBuilderCacheArgHandler(t *testing.T) {
	got, _, err := builderCacheArgHandler(`type=local,src=C:\in,dest=C:\out`)
	assert.NilError(t, err)
	assert.Equal(t, got, "type=local,src=/mnt/c/in,dest=/mnt/c/out")

	got, _, err = builderCacheArgHandler("type=registry,ref=example.com/cache")
	assert.NilError(t, err)
	assert.Equal(t, got, "type=registry,ref=example.com/cache")
}

func TestBuildContextArgHandler(t *testing.T) {
	got, _, err := buildContextArgHandler(`myctx=C:\ctx`)
	assert.NilError(t, err)
	assert.Equal(t, got, "myctx=/mnt/c/ctx")

	got, _, err = buildContextArgHandler("alpine=docker-image://alpine:3.20")
	assert.NilError(t, err)
	assert.Equal(t, got, "alpine=docker-image://alpine:3.20")
}
