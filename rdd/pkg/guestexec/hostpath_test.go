// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package guestexec

import (
	"fmt"
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

// withHostDriveCwds pretends each drive letter has the given current
// directory.
func withHostDriveCwds(t *testing.T, cwds map[string]string) {
	t.Helper()
	saved := hostDriveCwd
	hostDriveCwd = func(drive string) (string, error) {
		cwd, ok := cwds[drive]
		if !ok {
			return "", fmt.Errorf("no current directory on drive %q", drive)
		}
		return cwd, nil
	}
	t.Cleanup(func() { hostDriveCwd = saved })
}

func TestTranslateHostPath(t *testing.T) {
	withHostCwd(t, `C:\work`)
	withHostDriveCwds(t, map[string]string{"c": `C:\work`, "d": `D:\proj`})
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
		{`..\..\target`, "/mnt/c/target"},
		{`..\..\..\x\y`, "/mnt/c/x/y"},
		{`.`, "/mnt/c/work"},
		{`c:sub`, "/mnt/c/work/sub"},
		{`d:sub`, "/mnt/d/proj/sub"},
		{`D:..\..\x`, "/mnt/d/x"},
		{`d:`, "/mnt/d/proj"},
		{`\\server\share\file`, "//server/share/file"},
		{`\data`, "/mnt/c/data"},
		{`/data`, "/mnt/c/data"},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			got, err := TranslateHostPath(tc.arg)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestTranslateHostPathRootedUnderUNCCwd(t *testing.T) {
	cases := []struct {
		cwd  string
		want string
	}{
		{`\\server\share\dir`, "//server/share/data"},
		{`\\?\C:\work`, "//?/C:/data"},
	}
	for _, tc := range cases {
		t.Run(tc.cwd, func(t *testing.T) {
			withHostCwd(t, tc.cwd)
			for _, arg := range []string{`\data`, `/data`} {
				got, err := TranslateHostPath(arg)
				assert.NilError(t, err)
				assert.Equal(t, got, tc.want, "arg %q", arg)
			}
		})
	}
}

func TestIsDriveAbsolute(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`C:\data`, true},
		{`C:/data`, true},
		{`c:\`, true},
		{`z:/`, true},
		{`C:`, false},
		{`C:data`, false},
		{`\data`, false},
		{`/data`, false},
		{`data`, false},
		{`\\server\share`, false},
		{`\\?\C:\data`, false},
		{`1:\data`, false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, IsDriveAbsolute(tc.path), tc.want)
		})
	}
}
