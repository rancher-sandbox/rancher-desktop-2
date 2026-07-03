// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

import (
	"testing"

	"gotest.tools/v3/assert"
)

// TestTranslateCommandLine exercises the full parser against the generated
// nerdctl command table, with path translation as it happens on Windows.
func TestTranslateCommandLine(t *testing.T) {
	withHostCwd(t, `C:\work`)
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "volume flag on aliased run command",
			args: []string{"run", "--rm", "-v", `C:\data:/data`, "alpine", "ls", "--color"},
			want: []string{"run", "--rm", "-v", "/mnt/c/data:/data", "--", "alpine", "ls", "--color"},
		},
		{
			name: "foreign flags after the image are not parsed",
			args: []string{"run", "alpine", "--env", "x=y"},
			want: []string{"run", "--", "alpine", "--env", "x=y"},
		},
		{
			name: "bunched short flags stay bunched",
			args: []string{"run", "-it", "alpine"},
			want: []string{"run", "-it", "--", "alpine"},
		},
		{
			name: "mount flag",
			args: []string{"container", "run", "--mount", `type=bind,source=C:\x,target=/y`, "img"},
			want: []string{"container", "run", "--mount", "type=bind,source=/mnt/c/x,target=/y", "--", "img"},
		},
		{
			name: "global flag with equals sign",
			args: []string{"-n=test", "images"},
			want: []string{"-n", "test", "images"},
		},
		{
			name: "global flag before subcommand",
			args: []string{"--namespace", "test", "ps", "-a"},
			want: []string{"--namespace", "test", "ps", "-a"},
		},
		{
			name: "container cp positional host path",
			args: []string{"cp", `C:\host.txt`, "ctr:/tmp/x"},
			want: []string{"cp", "/mnt/c/host.txt", "ctr:/tmp/x"},
		},
		{
			name: "container cp from container",
			args: []string{"container", "cp", "ctr:/tmp/x", `C:\out`},
			want: []string{"container", "cp", "ctr:/tmp/x", "/mnt/c/out"},
		},
		{
			name: "build context and dockerfile",
			args: []string{"build", "-f", `C:\proj\Dockerfile`, "-t", "img", `C:\proj`},
			want: []string{"build", "-f", "/mnt/c/proj/Dockerfile", "-t", "img", "/mnt/c/proj"},
		},
		{
			name: "relative build context",
			args: []string{"build", "."},
			want: []string{"build", "/mnt/c/work"},
		},
		{
			name: "compose file flag",
			args: []string{"compose", "-f", `C:\app\compose.yaml`, "up", "-d"},
			want: []string{"compose", "-f", "/mnt/c/app/compose.yaml", "up", "-d"},
		},
		{
			name: "unknown subcommand passes through",
			args: []string{"internal", "oci-hook", "createRuntime"},
			want: []string{"internal", "oci-hook", "createRuntime"},
		},
		{
			name: "double dash stops parsing",
			args: []string{"image", "ls", "--", "-v"},
			want: []string{"image", "ls", "--", "-v"},
		},
		{
			name: "output path on image save",
			args: []string{"save", "-o", `C:\img.tar`, "alpine"},
			want: []string{"save", "-o", "/mnt/c/img.tar", "--", "alpine"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TranslateCommandLine(tc.args)
			assert.NilError(t, err)
			assert.DeepEqual(t, got.Args, tc.want)
			assert.NilError(t, got.RunCleanups())
		})
	}
}

func TestTranslateCommandLineErrors(t *testing.T) {
	_, err := TranslateCommandLine([]string{"run", "--no-such-flag", "alpine"})
	assert.ErrorContains(t, err, "does not support option --no-such-flag")

	_, err = TranslateCommandLine([]string{"container", "cp", "onlyone"})
	assert.ErrorContains(t, err, "accepts 2 args")
}

// TestTableSanity pins properties of the generated table that the parser
// relies on; a regeneration that loses them should fail here.
func TestTableSanity(t *testing.T) {
	for _, path := range []string{"container run", "container create", "container exec"} {
		assert.Assert(t, commandSpecs[path].foreignFlags, "command %q must have foreign flags", path)
	}
	for _, alias := range []string{"run", "build", "cp"} {
		_, ok := commandAliases[alias]
		assert.Assert(t, ok, "alias %q must exist", alias)
	}
	// The overlay init must have attached the positional handlers.
	assert.Assert(t, commands["container cp"].handler != nil)
	assert.Assert(t, commands["builder build"].handler != nil)
	// Path handlers must be wired, not left as ignoredArgHandler: translate
	// a volume argument through the live table.
	got, _, err := commands["container run"].options["-v"](`C:\x:/y`)
	assert.NilError(t, err)
	assert.Equal(t, got, "/mnt/c/x:/y")
}
