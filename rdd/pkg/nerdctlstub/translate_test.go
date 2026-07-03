// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

import (
	"testing"

	"gotest.tools/v3/assert"
)

// withHostCwd pretends the host works in the given directory.
func withHostCwd(t *testing.T, cwd string) {
	t.Helper()
	saved := hostCwd
	hostCwd = func() (string, error) { return cwd, nil }
	t.Cleanup(func() { hostCwd = saved })
}

func TestTranslateHostPath(t *testing.T) {
	withHostCwd(t, `C:\work`)
	cases := []struct {
		arg  string
		want string
	}{
		{`C:\Users\jan\app`, "/mnt/c/Users/jan/app"},
		{`c:/foo/bar`, "/mnt/c/foo/bar"},
		{`C:\foo\..\bar\.\baz`, "/mnt/c/bar/baz"},
		{`D:\`, "/mnt/d/"},
		{`sub\dir`, "/mnt/c/work/sub/dir"},
		{`..\sibling`, "/mnt/c/sibling"},
		{`.`, "/mnt/c/work"},
		{`\\server\share\file`, "//server/share/file"},
		{`/already/posix`, "/already/posix"},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			got, err := TranslateHostPath(tc.arg)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestVolumeArgHandler(t *testing.T) {
	cases := []struct {
		arg  string
		want string
	}{
		{`C:\data:/data`, "/mnt/c/data:/data"},
		{`C:\data:/data:ro`, "/mnt/c/data:/data:ro"},
		{`C:\data:/data:rw`, "/mnt/c/data:/data:rw"},
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
