// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package gpu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"
)

// serveInputs points inputs at a local server and returns the request counter.
func serveInputs(t *testing.T, files map[string]string) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	orig := inputs
	t.Cleanup(func() { inputs = orig })
	inputs = nil
	for path, body := range files {
		inputs = append(inputs, input{path: path[1:], url: srv.URL + path, sha256: checksum([]byte(body))})
	}
	return &hits
}

func TestStageFetchesInputsOnce(t *testing.T) {
	hits := serveInputs(t, map[string]string{"/a.tar.gz": "A", "/winsdk/h.h": "H"})
	dir := t.TempDir()

	assert.NilError(t, Stage(context.Background(), dir, ""))
	for rel, want := range map[string]string{
		"src/a.tar.gz":   "A",
		"src/winsdk/h.h": "H",
		"src/build-id":   BuildID,
		"src/image":      BuildImage,
		"src/build.sh":   string(buildScript),
	} {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		assert.NilError(t, err)
		assert.Equal(t, string(got), want, rel)
	}

	first := hits.Load()
	assert.NilError(t, Stage(context.Background(), dir, ""))
	assert.Equal(t, hits.Load(), first, "second Stage must not download again")
}

func TestStageRejectsChecksumMismatch(t *testing.T) {
	serveInputs(t, map[string]string{"/a": "A"})
	inputs[0].sha256 = checksum([]byte("different"))

	err := Stage(context.Background(), t.TempDir(), "")
	assert.ErrorContains(t, err, "checksum mismatch")
}

func TestStageDropsStaleLibrary(t *testing.T) {
	serveInputs(t, map[string]string{"/a": "A"})
	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, LibName), []byte("old"), 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, buildIDMarker), []byte("older-build"), 0o644))

	assert.NilError(t, Stage(context.Background(), dir, ""))
	_, err := os.Stat(filepath.Join(dir, LibName))
	assert.Assert(t, os.IsNotExist(err), "stale library must be removed so the guest rebuilds")
}

func TestStageKeepsCurrentBuild(t *testing.T) {
	hits := serveInputs(t, map[string]string{"/a": "A"})
	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, LibName), []byte("lib"), 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, buildIDMarker), []byte(BuildID), 0o644))

	assert.NilError(t, Stage(context.Background(), dir, ""))
	assert.Equal(t, hits.Load(), int32(0), "a current build needs no downloads")
}

func TestStageFromSource(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// A real ROCm install uses a versioned library name.
	assert.NilError(t, os.WriteFile(filepath.Join(src, "librocdxg.so.1.2.2"), []byte("LIB"), 0o644))

	assert.NilError(t, Stage(context.Background(), dst, src))

	got, err := os.ReadFile(filepath.Join(dst, LibName))
	assert.NilError(t, err)
	assert.Equal(t, string(got), "LIB", "versioned lib should be normalized to %s", LibName)
	assert.Assert(t, !built(dst), "a source override must not look like a current build")
}

func TestStageFromSourceMissingLibrary(t *testing.T) {
	err := Stage(context.Background(), t.TempDir(), t.TempDir())
	assert.ErrorContains(t, err, LibName)
}

func TestUnstage(t *testing.T) {
	serveInputs(t, map[string]string{"/a": "A"})
	dir := t.TempDir()
	assert.NilError(t, Stage(context.Background(), dir, ""))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, LibName), []byte("lib"), 0o755))

	assert.NilError(t, Unstage(dir))
	entries, err := os.ReadDir(dir)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 0, "Unstage must leave the directory empty")
	assert.NilError(t, Unstage(dir), "Unstage is idempotent")
}
