// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/guestexec"
)

func TestVolumeArgHandler(t *testing.T) {
	cases := []struct {
		arg  string
		want string
	}{
		{`C:\data:/data`, "/mnt/c/data:/data"},
		{`C:\data:/data:ro`, "/mnt/c/data:/data:ro"},
		{`C:\data:/data:rw`, "/mnt/c/data:/data:rw"},
		{`\\?\C:\data:/data:ro`, "/mnt/c/data:/data:ro"},
		{`myvol:/data`, "myvol:/data"},
		{`my-vol_1.0:/data:ro`, "my-vol_1.0:/data:ro"},
		// Anonymous volumes reach nerdctl unchanged.
		{`no-separator`, "no-separator"},
		{`:foo`, ":foo"},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			got, err := volumeArgHandler(tc.arg)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}

	for _, arg := range []string{`C:\data`, `C:/data`, `d:app`, `\\?\C:\data`, `//?/C:/data`} {
		_, err := volumeArgHandler(arg)
		assert.ErrorContains(t, err, "expected source:container-path", "%s", arg)
	}
}

func TestVolumeArgHandlerModes(t *testing.T) {
	cases := []struct {
		arg  string
		want string
	}{
		{`/data`, "/data"},
		{`:/data`, ":/data"},
		{`myvol:/data:z`, "myvol:/data:z"},
		{`myvol:/data:rro`, "myvol:/data:rro"},
		{`myvol:/data:ro,rshared`, "myvol:/data:ro,rshared"},
		{`C:\data:/data:z`, "/mnt/c/data:/data:z"},
		{`C:\src:/data:z:ro`, "/mnt/c/src:/data:z:ro"},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			got, err := volumeArgHandler(tc.arg)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}

	// The drive-relative source `d:app` must reach TranslateHostPath whole.
	want, err := guestexec.TranslateHostPath("d:app")
	assert.NilError(t, err)
	got, err := volumeArgHandler("d:app:/data")
	assert.NilError(t, err)
	assert.Equal(t, got, want+":/data")
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
			name: "bind mount of an extended-length drive path",
			arg:  `type=bind,source=\\?\C:\x,target=/y`,
			want: "type=bind,source=/mnt/c/x,target=/y",
		},
		{
			name: "volume name stays unchanged",
			arg:  `type=volume,source=vol,target=/y`,
			want: "type=volume,source=vol,target=/y",
		},
		{
			name: "bind mount of a volume name stays unchanged",
			arg:  `type=bind,source=vol,target=/y`,
			want: "type=bind,source=vol,target=/y",
		},
		{
			name: "keys in any case, as nerdctl lowercases them",
			arg:  `Type=bind,Source=C:\x,target=/y`,
			want: "Type=bind,Source=/mnt/c/x,target=/y",
		},
		{
			name: "default volume type with a host path",
			arg:  `source=C:\x,target=/y`,
			want: "source=/mnt/c/x,target=/y",
		},
		{
			name: "volume type with a host path",
			arg:  `type=volume,source=C:\x,target=/y`,
			want: "type=volume,source=/mnt/c/x,target=/y",
		},
		{
			name: "volume type does not override bind",
			arg:  `type=bind,type=volume,source=C:\x,target=/y`,
			want: "type=bind,type=volume,source=/mnt/c/x,target=/y",
		},
		{
			name: "tmpfs ignores the source",
			arg:  `type=tmpfs,source=C:\x,target=/y`,
			want: `type=tmpfs,source=C:\x,target=/y`,
		},
		{
			name: "last of bind and tmpfs wins",
			arg:  `type=tmpfs,type=bind,source=C:\x,target=/y`,
			want: "type=tmpfs,type=bind,source=/mnt/c/x,target=/y",
		},
		{
			name: "empty source stays unchanged",
			arg:  `type=bind,source=,target=/y`,
			want: "type=bind,source=,target=/y",
		},
		{
			name: "volume name after leading colons",
			arg:  `source=:vol,target=/y`,
			want: "source=:vol,target=/y",
		},
		{
			name: "volume name before a colon",
			arg:  `source=vol:/z`,
			want: "source=vol:/z",
		},
		{
			name: "host path before a colon",
			arg:  `source=C:\x:/z`,
			want: "source=/mnt/c/x:/z",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mountArgHandler(tc.arg)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestStdinArgPassesThrough(t *testing.T) {
	got, err := TranslateCommandLine([]string{"build", "-f", "-", `C:\ctx`})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"build", "-f", "-", "/mnt/c/ctx"})

	got, err = TranslateCommandLine([]string{"compose", "-f", "-", "up"})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"compose", "-f", "-", "up"})

	// A lone `-` is a positional argument, not an option.
	got, err = TranslateCommandLine([]string{"build", "-", "--build-context", `x=C:\dir`})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"build", "--build-context", "x=/mnt/c/dir", "-"})

	for _, arg := range []string{"type=tar,dest=-", "type=docker,dest=-", "type=tar,tar=false,dest=-"} {
		got, err := builderCacheArgHandler(arg)
		assert.NilError(t, err)
		assert.Equal(t, got, arg)
	}

	// Elsewhere `-` names a file or directory, such as `--cidfile -`.
	want, err := guestexec.TranslateHostPath("-")
	assert.NilError(t, err)
	got, err = TranslateCommandLine([]string{"run", "--cidfile", "-", "alpine"})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"run", "--cidfile", want, "--", "alpine"})
	for _, arg := range []string{"type=local,dest=-", "type=oci,tar=false,dest=-", "type=oci,tar=0,dest=-"} {
		got, err := builderCacheArgHandler(arg)
		assert.NilError(t, err)
		assert.Equal(t, got, strings.TrimSuffix(arg, "-")+want)
	}

	// A drive path with a doubled slash is not a URL.
	got, err = TranslateCommandLine([]string{"build", "-f", `C://ctx/Dockerfile`, `C:\ctx`})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"build", "-f", "/mnt/c/ctx/Dockerfile", "/mnt/c/ctx"})
}

func TestBuilderCacheArgHandler(t *testing.T) {
	got, err := builderCacheArgHandler(`type=local,src=C:\in,dest=C:\out`)
	assert.NilError(t, err)
	assert.Equal(t, got, "type=local,src=/mnt/c/in,dest=/mnt/c/out")

	got, err = builderCacheArgHandler("type=registry,ref=example.com/cache")
	assert.NilError(t, err)
	assert.Equal(t, got, "type=registry,ref=example.com/cache")
}

func TestBuilderOutputAndSecret(t *testing.T) {
	for _, tc := range []struct {
		flag, arg, want string
	}{
		{"-o", `C:\out`, "/mnt/c/out"},
		{"--output", `type=local,dest=C:\out`, "type=local,dest=/mnt/c/out"},
		{"--secret", `id=s,source=C:\s.txt`, "id=s,source=/mnt/c/s.txt"},
		{"--secret", `id=s,src=C:\s.txt`, "id=s,src=/mnt/c/s.txt"},
		{"--secret", "id=s,type=env,src=MY_SECRET", "id=s,type=env,src=MY_SECRET"},
		{"--secret", "id=s,source=MY_SECRET,type=env", "id=s,source=MY_SECRET,type=env"},
		// buildctl lowercases keys.
		{"--secret", `id=s,Source=C:\s.txt`, "id=s,Source=/mnt/c/s.txt"},
		{"--secret", "Type=env,id=s,SRC=MY_SECRET", "Type=env,id=s,SRC=MY_SECRET"},
		{"--output", `type=local,DEST=C:\out`, "type=local,DEST=/mnt/c/out"},
		{"--output", "type=tar,DEST=-", "type=tar,DEST=-"},
		// buildctl takes `src=` as a file unless the last `type=` is env
		// and the last `env=` is empty or missing.
		{"--secret", `id=s,type=env,env=FOO,src=C:\f.txt`, "id=s,type=env,env=FOO,src=/mnt/c/f.txt"},
		{"--secret", `id=s,type=env,type=file,src=C:\f.txt`, "id=s,type=env,type=file,src=/mnt/c/f.txt"},
		// buildctl decodes CSV quotes.
		{"--output", `"type=tar",dest=-`, `"type=tar",dest=-`},
		{"--secret", `"type=env",id=s,src=MY_SECRET`, `"type=env",id=s,src=MY_SECRET`},
		{"--output", `type=local,"dest=C:\a,b"`, `type=local,"dest=/mnt/c/a,b"`},
		// buildctl drops a trailing line break and keeps any other line
		// break in its field.
		{"--secret", "id=s,src=C:\\s.txt\r\n", "id=s,src=/mnt/c/s.txt"},
		{"--output", "type=local,dest=C:\\out\n,mode=max", "type=local,\"dest=/mnt/c/out\n\",mode=max"},
	} {
		t.Run(tc.flag+" "+tc.arg, func(t *testing.T) {
			got, err := TranslateCommandLine([]string{"build", tc.flag, tc.arg, `C:\ctx`})
			assert.NilError(t, err)
			assert.DeepEqual(t, got, []string{"build", tc.flag, tc.want, "/mnt/c/ctx"})
		})
	}

	// nerdctl wraps a value without a lowercase `type=` in
	// `type=local,dest=`, and buildctl takes the last `dest=` it then sees.
	first, err := guestexec.TranslateHostPath("Type=local")
	assert.NilError(t, err)
	got, err := TranslateCommandLine([]string{"build", "-o", `Type=local,Dest=C:\out`, `C:\ctx`})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"build", "-o", first + ",Dest=/mnt/c/out", "/mnt/c/ctx"})

	// A relative path can take `type=` from the working directory, and then
	// nerdctl does not wrap the value.
	dir := filepath.Join(t.TempDir(), "type=x")
	assert.NilError(t, os.Mkdir(dir, 0o755))
	t.Chdir(dir)
	out, err := guestexec.TranslateHostPath("out")
	assert.NilError(t, err)
	got, err = TranslateCommandLine([]string{"build", "-o", "out", `C:\ctx`})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{"build", "-o", "type=local,dest=" + out, "/mnt/c/ctx"})

	// A relative path from a working directory with a comma gets CSV quotes.
	dir = filepath.Join(t.TempDir(), "a,b")
	assert.NilError(t, os.Mkdir(dir, 0o755))
	t.Chdir(dir)
	out, err = guestexec.TranslateHostPath("out")
	assert.NilError(t, err)
	secret, err := guestexec.TranslateHostPath("s.txt")
	assert.NilError(t, err)
	got, err = TranslateCommandLine([]string{"build", "-o", "out", "--secret", "id=s,src=s.txt", `C:\ctx`})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, []string{
		"build", "-o", `type=local,"dest=` + out + `"`,
		"--secret", `id=s,"src=` + secret + `"`, "/mnt/c/ctx",
	})
}

func TestBuildContextArgHandler(t *testing.T) {
	got, err := buildContextArgHandler(`myctx=C:\ctx`)
	assert.NilError(t, err)
	assert.Equal(t, got, "myctx=/mnt/c/ctx")

	got, err = buildContextArgHandler("alpine=docker-image://alpine:3.20")
	assert.NilError(t, err)
	assert.Equal(t, got, "alpine=docker-image://alpine:3.20")

	// A comma belongs to the path; nerdctl splits only at the first `=`.
	got, err = buildContextArgHandler(`myctx=C:\a,b=c`)
	assert.NilError(t, err)
	assert.Equal(t, got, "myctx=/mnt/c/a,b=c")

	got, err = buildContextArgHandler(`layout=oci-layout://C:\layout`)
	assert.NilError(t, err)
	assert.Equal(t, got, "layout=oci-layout:///mnt/c/layout")

	// nerdctl rejects a value without `=` itself.
	got, err = buildContextArgHandler("foo")
	assert.NilError(t, err)
	assert.Equal(t, got, "foo")
}

func TestTranslateCommandLinePathForms(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
	}{
		{
			[]string{"pull", "--cosign-key", "awskms:///alias/key", "alpine"},
			[]string{"pull", "--cosign-key", "awskms:///alias/key", "--", "alpine"},
		},
		{
			[]string{"run", "--cosign-key", "k8s://ns/secret", "alpine"},
			[]string{"run", "--cosign-key", "k8s://ns/secret", "--", "alpine"},
		},
		{
			[]string{"push", "--cosign-key", `C:\cosign.key`, "example.com/img"},
			[]string{"push", "--cosign-key", "/mnt/c/cosign.key", "--", "example.com/img"},
		},
		{
			[]string{"pull", "--cosign-key", "pkcs11:token=tok;object=key", "alpine"},
			[]string{"pull", "--cosign-key", "pkcs11:token=tok;object=key", "--", "alpine"},
		},
		{
			[]string{"run", "--env-file", `C:\a.env,D:\b.env`, "alpine"},
			[]string{"run", "--env-file", "/mnt/c/a.env,/mnt/d/b.env", "--", "alpine"},
		},
		{
			[]string{"run", "--env-file", `C:\work\base.env,C:\work\env\..\override.env`, "alpine"},
			[]string{"run", "--env-file", "/mnt/c/work/base.env,/mnt/c/work/override.env", "--", "alpine"},
		},
		{
			[]string{"run", "--env-file", `"C:\a,b.env",C:\c.env`, "alpine"},
			[]string{"run", "--env-file", `"/mnt/c/a,b.env",/mnt/c/c.env`, "--", "alpine"},
		},
		{
			[]string{"build", "--ssh", "default=", `C:\ctx`},
			[]string{"build", "--ssh", "default=", "/mnt/c/ctx"},
		},
		{
			[]string{"build", "--ssh", "default", `C:\ctx`},
			[]string{"build", "--ssh", "default", "/mnt/c/ctx"},
		},
		{
			[]string{"build", "--ssh", `default=C:\key1,C:\key2`, `C:\ctx`},
			[]string{"build", "--ssh", "default=/mnt/c/key1,/mnt/c/key2", "/mnt/c/ctx"},
		},
		{
			[]string{"build", "--ssh", `agent=C:\agent.sock,raw=true`, `C:\ctx`},
			[]string{"build", "--ssh", "agent=/mnt/c/agent.sock,raw=true", "/mnt/c/ctx"},
		},
		{
			[]string{"builder", "debug", "--ssh", `id=C:\key`, `C:\ctx`},
			[]string{"builder", "debug", "--ssh", "id=/mnt/c/key", "/mnt/c/ctx"},
		},
		{
			[]string{"build", "--", `C:\ctx`, "-x"},
			[]string{"build", "--", "/mnt/c/ctx", "-x"},
		},
		{
			[]string{"build", "-f", `C:\ctx\sub\`, `C:\ctx`},
			[]string{"build", "-f", "/mnt/c/ctx/sub/", "/mnt/c/ctx"},
		},
		{
			[]string{"build", "-f", `C:\ctx\.`, `C:\ctx`},
			[]string{"build", "-f", "/mnt/c/ctx/.", "/mnt/c/ctx"},
		},
		{
			[]string{"build", "-f", `D:\.`, `C:\ctx`},
			[]string{"build", "-f", "/mnt/d/.", "/mnt/c/ctx"},
		},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got, err := TranslateCommandLine(tc.args)
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

func TestKeepDirSuffix(t *testing.T) {
	for _, tc := range []struct {
		arg, guestPath, want string
	}{
		{"d:", "/mnt/d/x", "/mnt/d/x/."},
		{"d:.", "/mnt/d/x", "/mnt/d/x/."},
		{"d:", "/mnt/d/", "/mnt/d/."},
		{".", "/mnt/c/w", "/mnt/c/w/."},
		// An `image encrypt --key` value is keyfile:password.
		{"k.pem:.", "/mnt/c/w/k.pem:.", "/mnt/c/w/k.pem:."},
	} {
		t.Run(tc.arg+" "+tc.guestPath, func(t *testing.T) {
			assert.Equal(t, keepDirSuffix(tc.arg, tc.guestPath), tc.want)
		})
	}
}

func TestFilePathListArgHandlerRejects(t *testing.T) {
	// csv.Writer cannot write these lists so that pflag reads them back
	// unchanged.
	for _, arg := range []string{
		`""`,
		"\"C:\\a\r\r\nb.env\"",
		"C:\\work\\real.env,\"C:\\work\\x\r\r\ny.env\"",
	} {
		_, err := filePathListArgHandler(arg)
		assert.ErrorContains(t, err, "invalid path list", "%q", arg)
	}
}

func TestEmptyPathValuePassesThrough(t *testing.T) {
	// nerdctl takes an empty value as unset. Rewritten, an empty --output
	// would export to the working directory instead of building an image.
	for _, tc := range []struct {
		args, want []string
	}{
		{
			[]string{"build", "-o", "", `C:\ctx`},
			[]string{"build", "-o", "", "/mnt/c/ctx"},
		},
		{
			[]string{"run", "--cidfile", "", "alpine"},
			[]string{"run", "--cidfile", "", "--", "alpine"},
		},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got, err := TranslateCommandLine(tc.args)
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}
