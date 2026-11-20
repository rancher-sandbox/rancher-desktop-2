// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"gotest.tools/v3/assert"
)

// roundTripFunc adapts a function to the http.RoundTripper interface.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// newTestDockerEngineForTar returns a dockerEngine whose Docker client is
// wired up to respond to CopyFromContainer with the given tar archive bytes,
// reporting the given stat for the archive's root entry.
func newTestDockerEngineForTar(t *testing.T, tarBytes []byte, isDir bool) *dockerEngine {
	t.Helper()

	stat := container.PathStat{Name: "export", Mode: 0o644}
	if isDir {
		stat.Mode = os.ModeDir | 0o755
	}
	statJSON, err := json.Marshal(stat)
	assert.NilError(t, err)
	statHeader := base64.StdEncoding.EncodeToString(statJSON)

	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// The client negotiates the API version via a HEAD/GET to /_ping
		// before issuing the real request; satisfy that here too.
		if req.URL.Path == "/_ping" {
			h := http.Header{}
			h.Set("API-Version", mobyclient.MaxAPIVersion)
			h.Set("OSType", "linux") // Always pretend to be a Linux host.
			return &http.Response{StatusCode: http.StatusOK, Header: h, Body: http.NoBody, Request: req}, nil
		}
		h := http.Header{}
		h.Set("X-Docker-Container-Path-Stat", statHeader)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     h,
			Body:       io.NopCloser(bytes.NewReader(tarBytes)),
			Request:    req,
		}, nil
	})

	cli, err := mobyclient.New(
		mobyclient.WithHost("tcp://127.0.0.1:0"),
		mobyclient.WithHTTPClient(&http.Client{Transport: transport}),
	)
	assert.NilError(t, err)

	d := &dockerEngine{}
	d.cli.Store(cli)
	return d
}

// buildTar creates a tar archive in memory from the given entries, in order.
type tarEntry struct {
	name     string
	typeflag byte
	linkname string
	content  string
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := int64(0o644)
		if typeflag == tar.TypeDir {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: typeflag,
			Linkname: e.linkname,
			Size:     int64(len(e.content)),
			Mode:     mode,
		}
		assert.NilError(t, tw.WriteHeader(hdr))
		if e.content != "" {
			_, err := tw.Write([]byte(e.content))
			assert.NilError(t, err)
		}
	}
	assert.NilError(t, tw.Close())
	return buf.Bytes()
}

// runExport runs dockerEngine.export to completion (or until an error is
// reported) and returns the first error seen, if any.
func runExport(t *testing.T, d *dockerEngine, destDir, sourcePath string, isDirectory bool) error {
	t.Helper()
	ch := make(chan result[engineExportProgress], 1)
	err := d.export(t.Context(), engineExportOptions{
		id:          "fake-container-id",
		destDir:     destDir,
		sourcePath:  sourcePath,
		isDirectory: isDirectory,
		ch:          ch,
	})
	assert.NilError(t, err)

	var lastErr error
	for res := range ch {
		if res.err != nil {
			lastErr = res.err
		}
	}
	return lastErr
}

func TestDockerEngineExportRejectsParentDirectoryTraversal(t *testing.T) {
	destDir := t.TempDir()
	tarBytes := buildTar(t, []tarEntry{
		{name: "../../etc/passwd", content: "malicious"},
	})
	d := newTestDockerEngineForTar(t, tarBytes, true)

	err := runExport(t, d, destDir, "/export", true)
	assert.ErrorContains(t, err, "invalid relative path")

	// Nothing should have been written outside destDir.
	_, statErr := os.Stat(filepath.Join(filepath.Dir(destDir), "etc", "passwd"))
	assert.Assert(t, os.IsNotExist(statErr))
}

func TestDockerEngineExportDropAbsolutePath(t *testing.T) {
	destDir := t.TempDir()
	hostDir := t.TempDir()
	hostPath := filepath.Join(hostDir, "missing")
	absolutePath := hostPath
	if runtime.GOOS == "windows" {
		// On Windows, drop the drive, and convert to slashes.
		absolutePath = filepath.ToSlash(strings.TrimPrefix(hostPath, filepath.VolumeName(hostPath)))
	}
	tarBytes := buildTar(t, []tarEntry{
		{name: absolutePath, content: "malicious"},
	})
	d := newTestDockerEngineForTar(t, tarBytes, true)

	// An absolute path is cleaned to a relative path, which is local, so this
	// should NOT be rejected as invalid; it is still confined under destDir. This
	// test documents that behavior and confirms the file lands inside destDir,
	// not at the host's absolute path.  We use a temporary directory to ensure
	// that if the test fails, we do not clobber anything important.
	assert.NilError(t, runExport(t, d, destDir, "/export", true))

	content, err := os.ReadFile(filepath.Join(destDir, absolutePath[1:]))
	assert.NilError(t, err)
	assert.Equal(t, string(content), "malicious")
	_, err = os.Stat(hostPath)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestDockerEngineExportIgnoresSymlinkEntriesAndStaysConfined(t *testing.T) {
	destDir := t.TempDir()
	hostDir := t.TempDir()
	absoluteDir := hostDir
	if runtime.GOOS == "windows" {
		// On Windows, drop the drive, and convert to slashes.
		absoluteDir = filepath.ToSlash(strings.TrimPrefix(hostDir, filepath.VolumeName(hostDir)))
	}
	tarBytes := buildTar(t, []tarEntry{
		{name: "escape", typeflag: tar.TypeSymlink, linkname: "../../../.." + absoluteDir},
		{name: "escape/file", content: "malicious"},
	})
	d := newTestDockerEngineForTar(t, tarBytes, true)

	// The symlink entry itself is silently skipped (export only handles
	// regular files and directories), and the parent-directory creation for
	// the following entry creates a real directory confined to destDir
	// rather than following the symlink's target, so this does not escape.
	assert.NilError(t, runExport(t, d, destDir, "/export", true))

	content, err := os.ReadFile(filepath.Join(destDir, "escape", "file"))
	assert.NilError(t, err)
	assert.Equal(t, string(content), "malicious")

	_, err = os.Stat(filepath.Join(hostDir, "file"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestDockerEngineExportExtractsNestedDirectoriesNormally(t *testing.T) {
	destDir := t.TempDir()
	tarBytes := buildTar(t, []tarEntry{
		{name: "export", typeflag: tar.TypeDir},
		{name: "export/sub", typeflag: tar.TypeDir},
		{name: "export/sub/file.txt", content: "hello world"},
	})
	d := newTestDockerEngineForTar(t, tarBytes, true)

	err := runExport(t, d, destDir, "/export", true)
	assert.NilError(t, err)

	content, readErr := os.ReadFile(filepath.Join(destDir, "sub", "file.txt"))
	assert.NilError(t, readErr)
	assert.Equal(t, string(content), "hello world")

	info, statErr := os.Stat(filepath.Join(destDir, "sub"))
	assert.NilError(t, statErr)
	assert.Assert(t, info.IsDir())
}
