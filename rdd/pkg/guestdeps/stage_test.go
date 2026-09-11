// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package guestdeps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

const (
	testVersion  = "0.2.7"
	testFilename = "distro.v" + testVersion + ".amd64.raw.xz"
)

// An assetServer stands in for the release the manifest points at. It counts
// requests, so a test can show that a download happened once, was retried, or
// never happened at all, and it can fail the first few requests to stand in for
// a flaky transfer.
type assetServer struct {
	requests atomic.Int32
	failures []int
	body     []byte
	url      string
}

func newAssetServer(t *testing.T, body []byte, failures ...int) *assetServer {
	t.Helper()
	s := &assetServer{body: body, failures: failures}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n := int(s.requests.Add(1)); n <= len(s.failures) {
			w.WriteHeader(s.failures[n-1])
			return
		}
		_, _ = w.Write(s.body)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL + "/" + testFilename
	return s
}

// newStallingServer answers with the response headers and the first byte of the
// body, then stops sending. It stands in for a peer that accepts the request and
// goes quiet, the failure no transport timeout catches.
func newStallingServer(t *testing.T, body []byte) *assetServer {
	t.Helper()
	s := &assetServer{body: body}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body[:1])
		w.(http.Flusher).Flush()
		<-release
	}))
	// Cleanups run last in, first out, so the handlers unblock before Close
	// waits for them.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	s.url = srv.URL + "/" + testFilename
	return s
}

// waitForRequest blocks until the server has taken a request, so a test can act
// on a transfer that is under way.
func (s *assetServer) waitForRequest(t *testing.T) {
	t.Helper()
	for range 2000 {
		if s.requests.Load() > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	assert.Assert(t, false, "the asset server was never asked for the download")
}

// dependency describes the served asset, claiming the checksum of want. Every
// test but the corruption one passes the bytes the server actually serves.
func (s *assetServer) dependency(want []byte) Dependency {
	sum := sha256.Sum256(want)
	return Dependency{
		Name:    "distro",
		Version: testVersion,
		Asset: Asset{
			Platform: "linux",
			Arch:     "amd64",
			Variant:  "raw",
			URL:      s.url,
			Checksum: "sha256:" + hex.EncodeToString(sum[:]),
		},
	}
}

// stagerFor returns a stager writing under a temporary directory, along with
// the cache directory and the path a staged asset is written to.
func stagerFor(t *testing.T, log io.Writer) (stager *Stager, cacheDir, destPath string) {
	t.Helper()
	dir := t.TempDir()
	cacheDir = filepath.Join(dir, "cache")
	return &Stager{CacheDir: cacheDir, Log: log}, cacheDir, filepath.Join(dir, "embedded", "distro.raw.xz")
}

// shortenRetries collapses the retry schedule so a test that exercises it runs
// in milliseconds rather than in the seconds a real build waits.
func shortenRetries(t *testing.T) {
	t.Helper()
	delay, ceiling := retryDelay, maxRetryDelay
	retryDelay, maxRetryDelay = time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { retryDelay, maxRetryDelay = delay, ceiling })
}

// shortenStallTimeout brings the deadline on the body read within a test's
// patience. httpClient keeps the response-header deadline it was built with;
// shortenHeaderTimeout moves that one.
func shortenStallTimeout(t *testing.T) {
	t.Helper()
	old := stallTimeout
	stallTimeout = 50 * time.Millisecond
	t.Cleanup(func() { stallTimeout = old })
}

// shortenHeaderTimeout brings the deadline on the response headers down as
// well, by rebuilding the client once stallTimeout is short, because that is
// what newHTTPClient reads.
func shortenHeaderTimeout(t *testing.T) {
	t.Helper()
	shortenStallTimeout(t)
	old := httpClient
	httpClient = newHTTPClient()
	t.Cleanup(func() { httpClient = old })
}

// shortenDownloadTimeout brings the deadline covering every attempt at one
// asset within a test's patience.
func shortenDownloadTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := downloadTimeout
	downloadTimeout = d
	t.Cleanup(func() { downloadTimeout = old })
}

// newTricklingServer answers and then sends one byte every gap, forever. It
// stands in for a throttled peer, too slow to finish and too regular to stall.
func newTricklingServer(t *testing.T, gap time.Duration) *assetServer {
	t.Helper()
	s := &assetServer{}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for {
			_, _ = w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-release:
				return
			case <-time.After(gap):
			}
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	s.url = srv.URL + "/" + testFilename
	return s
}

// newThrottlingServer refuses the first request with status and a Retry-After
// naming its own delay, then serves the asset.
func newThrottlingServer(t *testing.T, body []byte, status int) *assetServer {
	t.Helper()
	s := &assetServer{body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if s.requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write(s.body)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL + "/" + testFilename
	return s
}

func cachedAsset(cacheDir string) string {
	return filepath.Join(cacheDir, "distro", "v"+testVersion, testFilename)
}

func assertContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(got, want), "%s holds %d bytes, want %d", path, len(got), len(want))
}

// assertNoTempFiles guards the atomic writes: neither a completed nor an
// abandoned transfer may leave a partial file behind. It walks rather than
// globbing a fixed depth, so it keeps guarding through a change to the cache
// layout, and it covers the destination directory, where copyFile writes its
// temporary files one level higher.
func assertNoTempFiles(t *testing.T, dirs ...string) {
	t.Helper()
	var found []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
			switch {
			case errors.Is(err, fs.ErrNotExist):
				return nil // a failed stage may never create the directory
			case err != nil:
				return err
			case !entry.IsDir() && strings.Contains(entry.Name(), ".tmp-"):
				found = append(found, name)
			}
			return nil
		})
		assert.NilError(t, err)
	}
	assert.Assert(t, len(found) == 0, "leftover temp files: %v", found)
}

func TestStageDownloadsAndCaches(t *testing.T) {
	body := bytes.Repeat([]byte("distro image\n"), 1000)
	server := newAssetServer(t, body)
	dep := server.dependency(body)
	stager, cacheDir, destPath := stagerFor(t, nil)

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assertContent(t, destPath, body)
	assertContent(t, cachedAsset(cacheDir), body)
	assert.Equal(t, server.requests.Load(), int32(1))
	assertNoTempFiles(t, cacheDir, filepath.Dir(destPath))
}

// A second build stages from the cache: the bytes are already verified, so it
// must not go back to the network even when the staged copy is gone.
func TestStageReusesTheCachedDownload(t *testing.T) {
	body := []byte("distro image")
	server := newAssetServer(t, body)
	dep := server.dependency(body)
	stager, _, destPath := stagerFor(t, nil)

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assert.NilError(t, os.Remove(destPath))
	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))

	assertContent(t, destPath, body)
	assert.Equal(t, server.requests.Load(), int32(1))
}

// A staged file that is already the asset is left alone; copying hundreds of
// megabytes on every build would be the whole cost of the download again.
func TestStageLeavesAnUpToDateFileAlone(t *testing.T) {
	body := []byte("distro image")
	server := newAssetServer(t, body)
	dep := server.dependency(body)
	var log bytes.Buffer
	stager, _, destPath := stagerFor(t, &log)

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	before, err := os.Stat(destPath)
	assert.NilError(t, err)

	log.Reset()
	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	after, err := os.Stat(destPath)
	assert.NilError(t, err)

	assert.Assert(t, after.ModTime().Equal(before.ModTime()), "the staged file was rewritten")
	assert.Assert(t, bytes.Contains(log.Bytes(), []byte("is up to date")), "log: %s", log.String())
}

// Bytes that fail verification are dropped. Nothing is cached, nothing is
// staged, and no retry follows.
func TestStageRejectsUnexpectedBytes(t *testing.T) {
	server := newAssetServer(t, []byte("someone else's image"))
	dep := server.dependency([]byte("distro image"))
	stager, cacheDir, destPath := stagerFor(t, nil)

	err := stager.Stage(t.Context(), dep, destPath)
	assert.ErrorContains(t, err, "checksum is sha256:")
	assert.Equal(t, server.requests.Load(), int32(1), "a mismatched download must not be retried")

	_, statErr := os.Stat(destPath)
	assert.Assert(t, os.IsNotExist(statErr), "nothing may be staged from a failed download")
	assertNoTempFiles(t, cacheDir, filepath.Dir(destPath))
}

// A stale staged file (an older distro version, or a truncated write from an
// interrupted build) is replaced rather than embedded as it stands.
func TestStageReplacesAStaleFile(t *testing.T) {
	body := []byte("distro image")
	server := newAssetServer(t, body)
	dep := server.dependency(body)
	stager, _, destPath := stagerFor(t, nil)

	assert.NilError(t, os.MkdirAll(filepath.Dir(destPath), 0o755))
	assert.NilError(t, os.WriteFile(destPath, []byte("the previous distro"), 0o644))

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assertContent(t, destPath, body)
}

func TestStageRetriesATransientFailure(t *testing.T) {
	shortenRetries(t)

	body := []byte("distro image")
	server := newAssetServer(t, body, http.StatusBadGateway, http.StatusTooManyRequests)
	dep := server.dependency(body)
	stager, _, destPath := stagerFor(t, nil)

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assertContent(t, destPath, body)
	assert.Equal(t, server.requests.Load(), int32(3))
}

// A URL the release does not have is a manifest bug; retrying it only delays
// the failure.
func TestStageDoesNotRetryAMissingAsset(t *testing.T) {
	server := newAssetServer(t, []byte("distro image"), http.StatusNotFound)
	dep := server.dependency([]byte("distro image"))
	stager, _, destPath := stagerFor(t, nil)

	err := stager.Stage(t.Context(), dep, destPath)
	assert.ErrorContains(t, err, "404")
	assert.Equal(t, server.requests.Load(), int32(1))
}

// A cache entry whose bytes do not match is downloaded again. Trusting the
// file's existence would stage bytes nothing has verified, and the build would
// embed them.
func TestStageReplacesACorruptCacheEntry(t *testing.T) {
	body := []byte("distro image")
	server := newAssetServer(t, body)
	dep := server.dependency(body)
	stager, cacheDir, destPath := stagerFor(t, nil)

	cachePath := cachedAsset(cacheDir)
	assert.NilError(t, os.MkdirAll(filepath.Dir(cachePath), 0o755))
	assert.NilError(t, os.WriteFile(cachePath, []byte("garbage"), 0o644))

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assertContent(t, destPath, body)
	assertContent(t, cachePath, body)
	assert.Equal(t, server.requests.Load(), int32(1), "the corrupt entry must be downloaded again")
	assertNoTempFiles(t, cacheDir, filepath.Dir(destPath))
}

// A staged file that already verifies needs no download, even once the cache is
// gone. macOS purges the cache directory under disk pressure, and a build that
// has the bytes must not go back to the network for them.
func TestStageSkipsTheDownloadWhenTheStagedFileIsCurrent(t *testing.T) {
	body := []byte("distro image")
	server := newAssetServer(t, body)
	dep := server.dependency(body)
	stager, cacheDir, destPath := stagerFor(t, nil)

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assert.NilError(t, os.RemoveAll(cacheDir))

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assertContent(t, destPath, body)
	assert.Equal(t, server.requests.Load(), int32(1), "a verified staged file must not be downloaded again")
}

// The attempt budget is spent and the last failure is reported with the url
// that failed.
func TestStageGivesUpAfterEveryAttemptFails(t *testing.T) {
	shortenRetries(t)

	body := []byte("distro image")
	failures := make([]int, downloadAttempts)
	for i := range failures {
		failures[i] = http.StatusBadGateway
	}
	server := newAssetServer(t, body, failures...)
	dep := server.dependency(body)
	stager, cacheDir, destPath := stagerFor(t, nil)

	err := stager.Stage(t.Context(), dep, destPath)
	assert.ErrorContains(t, err, "downloading "+server.url)
	assert.ErrorContains(t, err, "502")
	assert.Equal(t, server.requests.Load(), int32(downloadAttempts))
	assertNoTempFiles(t, cacheDir, filepath.Dir(destPath))
}

// A peer that answers and then goes quiet fails its attempt rather than
// blocking the build. Neither http.Client nor its transport bounds a body read,
// so without the stall timer the retry loop never runs at all.
func TestStageFailsAStalledTransfer(t *testing.T) {
	shortenRetries(t)
	shortenStallTimeout(t)

	body := []byte("distro image")
	server := newStallingServer(t, body)
	dep := server.dependency(body)
	stager, cacheDir, destPath := stagerFor(t, nil)

	done := make(chan error, 1)
	go func() { done <- stager.Stage(t.Context(), dep, destPath) }()

	select {
	case err := <-done:
		assert.ErrorContains(t, err, "no data received for")
		assert.Equal(t, server.requests.Load(), int32(downloadAttempts), "every attempt must stall and retry")
	case <-time.After(30 * time.Second):
		assert.Assert(t, false, "a stalled transfer blocked Stage; the retry loop never ran")
	}
	assertNoTempFiles(t, cacheDir, filepath.Dir(destPath))
}

// An interrupt during a transfer aborts it and cleans up, which is what the
// command promises the developer who hits Ctrl-C on a multi-minute download.
func TestStageCleansUpWhenCancelled(t *testing.T) {
	body := []byte("distro image")
	server := newStallingServer(t, body)
	dep := server.dependency(body)
	stager, cacheDir, destPath := stagerFor(t, nil)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- stager.Stage(ctx, dep, destPath) }()

	// Cancel once the transfer is under way, so the cleanup under test is the
	// one that runs with a temporary file already open.
	server.waitForRequest(t)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(30 * time.Second):
		assert.Assert(t, false, "Stage did not return after the context was cancelled")
	}
	assert.Equal(t, server.requests.Load(), int32(1), "a cancelled download must not be retried")
	_, statErr := os.Stat(destPath)
	assert.Assert(t, os.IsNotExist(statErr), "nothing may be staged from an interrupted download")
	assertNoTempFiles(t, cacheDir, filepath.Dir(destPath))
}

// Prune deletes inside the cache directory, so the name has to be one nothing
// else owns, clear of the app's cache and of anything instance.Name() produces.
func TestDefaultCacheDirIsNobodyElses(t *testing.T) {
	dir, err := DefaultCacheDir()
	assert.NilError(t, err)
	parent, err := os.UserCacheDir()
	assert.NilError(t, err)

	assert.Equal(t, dir, filepath.Join(parent, "rancher-desktop.guest-deps"))
	assert.Assert(t, !strings.HasPrefix(dir, filepath.Join(parent, "rancher-desktop-")),
		"%s is inside a directory instance.Name() can produce", dir)
}

// A peer sending just enough to restart the stall timer runs out the deadline
// covering every attempt.
func TestStageGivesUpOnATricklingTransfer(t *testing.T) {
	shortenRetries(t)
	shortenStallTimeout(t)
	shortenDownloadTimeout(t, time.Second)

	// A tenth of the stall window leaves the stall timer no chance to fire, so
	// the deadline is what ends this, and it ends the first attempt: any retry
	// here would mean the transfer stalled instead of running out of time.
	server := newTricklingServer(t, stallTimeout/10)
	dep := server.dependency([]byte("distro image"))
	stager, cacheDir, destPath := stagerFor(t, nil)

	done := make(chan error, 1)
	go func() { done <- stager.Stage(t.Context(), dep, destPath) }()

	select {
	case err := <-done:
		assert.ErrorContains(t, err, "gave up after "+downloadTimeout.String())
		assert.Equal(t, server.requests.Load(), int32(1))
	case <-time.After(30 * time.Second):
		assert.Assert(t, false, "a trickling transfer held Stage past its deadline")
	}
	assertNoTempFiles(t, cacheDir, filepath.Dir(destPath))
}

// The transport catches a peer that accepts the request and never answers. The
// stall timer starts on the body, which never arrives.
func TestStageFailsAPeerThatNeverSendsHeaders(t *testing.T) {
	shortenRetries(t)
	shortenHeaderTimeout(t)

	server := &assetServer{}
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		server.requests.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	server.url = srv.URL + "/" + testFilename

	dep := server.dependency([]byte("distro image"))
	stager, cacheDir, destPath := stagerFor(t, nil)

	done := make(chan error, 1)
	go func() { done <- stager.Stage(t.Context(), dep, destPath) }()

	select {
	case err := <-done:
		assert.ErrorContains(t, err, "timeout awaiting response headers")
		assert.Equal(t, server.requests.Load(), int32(downloadAttempts))
	case <-time.After(30 * time.Second):
		assert.Assert(t, false, "a peer that never answered held Stage; the header timeout never ran")
	}
	assertNoTempFiles(t, cacheDir, filepath.Dir(destPath))
}

// The progress line gives a percentage against the declared length, and the
// bytes alone when the server declared none.
func TestLogProgress(t *testing.T) {
	for name, tc := range map[string]struct {
		read, total int64
		want        string
	}{
		"against a declared length": {read: 100 << 20, total: 200 << 20, want: "Downloaded 100.0 MiB of 200.0 MiB (50%)\n"},
		"with no length to compare": {read: 100 << 20, total: -1, want: "Downloaded 100.0 MiB\n"},
	} {
		t.Run(name, func(t *testing.T) {
			var log bytes.Buffer
			(&Stager{Log: &log}).logProgress(tc.read, tc.total)
			assert.Equal(t, log.String(), tc.want)
		})
	}
}

// Reading reports once the interval has passed and then goes quiet until the
// next one. The reader is driven directly because the interval check compares
// two clock readings, and a real transfer of a test-sized body finishes inside
// one tick of the coarse monotonic clock Windows keeps.
func TestProgressReaderReportsOnInterval(t *testing.T) {
	var reads, totals []int64
	r := &progressReader{
		body:   bytes.NewReader(make([]byte, 64)),
		total:  200,
		next:   time.Now().Add(-time.Hour),
		report: func(read, total int64) { reads, totals = append(reads, read), append(totals, total) },
	}
	r.timer = time.AfterFunc(time.Hour, func() {})
	t.Cleanup(r.stop)

	n, err := r.Read(make([]byte, 64))
	assert.NilError(t, err)
	assert.Equal(t, n, 64)
	assert.DeepEqual(t, reads, []int64{64})
	assert.DeepEqual(t, totals, []int64{200})

	// The interval has just been reset, so the read after it says nothing.
	_, _ = r.Read(make([]byte, 8))
	assert.DeepEqual(t, reads, []int64{64})
}

// A server naming a delay is retried because of that header, even at a status
// that would otherwise be fatal. maxRetryDelay still caps the wait, so it is
// the ceiling, not the second the server named.
func TestStageRetriesAServerThatNamesADelay(t *testing.T) {
	shortenRetries(t)

	for name, status := range map[string]int{
		"too many requests": http.StatusTooManyRequests,
		// GitHub throttles release downloads with 403 too, and a 403 carrying no
		// delay is fatal, so the header is the whole difference here.
		"forbidden": http.StatusForbidden,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte("distro image")
			server := newThrottlingServer(t, body, status)
			dep := server.dependency(body)
			var log bytes.Buffer
			stager, _, destPath := stagerFor(t, &log)

			assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
			assertContent(t, destPath, body)
			assert.Equal(t, server.requests.Load(), int32(2))
			assert.Assert(t, strings.Contains(log.String(), "Retrying in "+maxRetryDelay.String()),
				"the named delay is consulted and capped, not replaced by the backoff; the log holds %q", log.String())
		})
	}
}

// A 408 says the request timed out, which a retry can fix, so it is not fatal.
func TestStageRetriesARequestTimeout(t *testing.T) {
	shortenRetries(t)

	body := []byte("distro image")
	server := newAssetServer(t, body, http.StatusRequestTimeout)
	dep := server.dependency(body)
	stager, _, destPath := stagerFor(t, nil)

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assertContent(t, destPath, body)
	assert.Equal(t, server.requests.Load(), int32(2))
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		value string
		want  time.Duration
		ok    bool
	}{
		"seconds":           {value: "120", want: 2 * time.Minute, ok: true},
		"http date":         {value: "Wed, 09 Sep 2026 12:00:30 GMT", want: 30 * time.Second, ok: true},
		"absent":            {value: ""},
		"malformed":         {value: "in a while"},
		"zero seconds":      {value: "0"},
		"date already past": {value: "Wed, 09 Sep 2026 11:59:00 GMT"},
		// Converting this many seconds to a Duration wraps negative, which would
		// retry at once instead of waiting.
		"seconds beyond a duration": {value: "31536000000", want: time.Duration(maxRetryAfterSeconds) * time.Second, ok: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := retryAfter(tc.value, now)
			assert.Equal(t, ok, tc.ok)
			if tc.ok {
				assert.Equal(t, got, tc.want)
			}
		})
	}
}

// The wait doubles per attempt, a server naming its own window overrides it, and
// both stop at the ceiling so a throttle cannot park the build.
func TestRetryWait(t *testing.T) {
	dropped := errors.New("connection reset")

	assert.Equal(t, retryWait(1, dropped), retryDelay)
	assert.Equal(t, retryWait(3, dropped), 4*retryDelay)
	assert.Equal(t, retryWait(9, dropped), maxRetryDelay)
	assert.Equal(t, retryWait(1, &retryAfterError{err: dropped, delay: 5 * time.Second}), 5*time.Second)
	assert.Equal(t, retryWait(1, &retryAfterError{err: dropped, delay: time.Hour}), maxRetryDelay)
}

// removeStaleEmptyDir treats an empty directory's timestamp as the moment it
// was created, which is what leaves a concurrent build's fresh directory alone.
// Go exposes no creation time on Linux, so the rule rests on mtime; this pins
// that on every platform the tests run on, NTFS included. The cutoff sits a
// minute back because a filesystem may coarsen timestamps, which the real
// seven-day cutoff is far too wide to notice.
func TestAFreshDirectoryOutlivesAPrune(t *testing.T) {
	cutoff := time.Now().Add(-time.Minute)
	dir := filepath.Join(t.TempDir(), "distro", "v"+testVersion)
	assert.NilError(t, os.MkdirAll(dir, 0o755))

	info, err := os.Stat(dir)
	assert.NilError(t, err)
	assert.Assert(t, info.ModTime().After(cutoff),
		"a directory created just now reports mtime %s, older than the %s cutoff", info.ModTime(), cutoff)

	removeStaleEmptyDir(dir, cutoff)
	_, err = os.Stat(dir)
	assert.NilError(t, err, "a directory younger than the cutoff must survive")
}

// An empty directory nothing has written to since the cutoff is the leftover of
// a version that has aged out, so it goes.
func TestPruneRemovesAStaleEmptyDirectory(t *testing.T) {
	stager, cacheDir, _ := stagerFor(t, nil)
	leftover := filepath.Join(cacheDir, "distro", "v0.2.6")
	assert.NilError(t, os.MkdirAll(leftover, 0o755))
	age(t, leftover)
	age(t, filepath.Dir(leftover))

	assert.NilError(t, stager.Prune())

	assertMissing(t, leftover)
	_, err := os.Stat(filepath.Dir(leftover))
	assert.NilError(t, err, "the dependency directory is not removed")
}

// Prune drops what no build has used for cacheTTL, whether that is a superseded
// download or the partial file a killed build left behind, and leaves the entry
// a build touched. The directory it empties keeps its own timestamp until the
// next run, so a second pass collects it.
func TestPruneRemovesUnusedDownloads(t *testing.T) {
	stager, cacheDir, _ := stagerFor(t, nil)
	superseded := writeCacheFile(t, cacheDir, "distro", "v0.2.6", "distro.v0.2.6.amd64.raw.xz")
	partial := writeCacheFile(t, cacheDir, "distro", "v"+testVersion, testFilename+".tmp-1234")
	current := writeCacheFile(t, cacheDir, "distro", "v"+testVersion, testFilename)
	age(t, superseded)
	age(t, partial)

	assert.NilError(t, stager.Prune())

	assertMissing(t, superseded)
	assertMissing(t, partial)
	_, err := os.Stat(current)
	assert.NilError(t, err, "an entry a build just used must survive")

	emptied := filepath.Join(cacheDir, "distro", "v0.2.6")
	_, err = os.Stat(emptied)
	assert.NilError(t, err, "the directory this run emptied is too new to collect yet")
	age(t, emptied)
	assert.NilError(t, stager.Prune())
	assertMissing(t, emptied)
}

// An empty directory a prune finds beside its own is not necessarily its own,
// so nothing at that level is removed.
func TestPruneLeavesForeignEmptyDirectoriesAlone(t *testing.T) {
	stager, cacheDir, _ := stagerFor(t, nil)
	stranger := filepath.Join(cacheDir, "GPUCache")
	assert.NilError(t, os.MkdirAll(stranger, 0o755))
	age(t, stranger)

	assert.NilError(t, stager.Prune())

	_, err := os.Stat(stranger)
	assert.NilError(t, err, "an empty directory this tool did not create must survive")
}

// Prune descends only the <name>/v<version>/ directories cachePath creates.
func TestPruneLeavesForeignFilesAlone(t *testing.T) {
	stager, cacheDir, _ := stagerFor(t, nil)
	strangers := []string{
		writeCacheFile(t, cacheDir, "updater-longhorn.json"),
		writeCacheFile(t, cacheDir, "Cache", "data_0"),
		writeCacheFile(t, cacheDir, "tls", "server.crt"),
		writeCacheFile(t, cacheDir, "distro", "notaversion", "keep-me"),
	}
	for _, path := range strangers {
		age(t, path)
	}

	assert.NilError(t, stager.Prune())

	for _, path := range strangers {
		_, err := os.Stat(path)
		assert.NilError(t, err, "%s is not this tool's to remove", path)
	}
}

// A first build has no cache to prune.
func TestPruneWithoutACache(t *testing.T) {
	stager, _, _ := stagerFor(t, nil)
	assert.NilError(t, stager.Prune())
}

// Staging marks the entry it used, both when it copies from the cache and when
// it finds the staged file already current, so an entry a build still depends
// on stops looking stale to the next prune.
func TestStageTouchesTheCacheEntryItUses(t *testing.T) {
	body := []byte("distro image")
	server := newAssetServer(t, body)
	dep := server.dependency(body)
	stager, cacheDir, destPath := stagerFor(t, nil)
	cachePath := cachedAsset(cacheDir)

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))

	// Staging from the cache.
	age(t, cachePath)
	assert.NilError(t, os.Remove(destPath))
	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assert.NilError(t, stager.Prune())
	_, err := os.Stat(cachePath)
	assert.NilError(t, err, "the entry this build copied from must survive a prune")

	// Finding the staged file already current.
	age(t, cachePath)
	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assert.NilError(t, stager.Prune())
	_, err = os.Stat(cachePath)
	assert.NilError(t, err, "the entry backing an up-to-date build must survive a prune")
}

// age backdates path beyond cacheTTL, so the next prune would collect it.
func age(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-cacheTTL - time.Hour)
	assert.NilError(t, os.Chtimes(path, old, old))
}

func writeCacheFile(t *testing.T, cacheDir string, elem ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{cacheDir}, elem...)...)
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte("cached bytes"), 0o644))
	return path
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	assert.Assert(t, os.IsNotExist(err), "%s should have been pruned", path)
}

// A directory whose name only starts with "v" is not a version directory, so
// nothing inside it is a download to collect.
func TestPruneLeavesAVendorDirectoryAlone(t *testing.T) {
	stager, cacheDir, _ := stagerFor(t, nil)
	stranger := writeCacheFile(t, cacheDir, "distro", "vendor", "config.json")
	age(t, stranger)

	assert.NilError(t, stager.Prune())

	_, err := os.Stat(stranger)
	assert.NilError(t, err, "a vendor directory holds no downloads")
}

// Builds share the cache, so one of them removing a directory underfoot must
// not stop the rest of the sweep.
func TestPruneContinuesPastAnUnreadableDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not restrict listing on Windows")
	}
	stager, cacheDir, _ := stagerFor(t, nil)
	// "aaa" sorts before "distro", so it is swept first.
	writeCacheFile(t, cacheDir, "aaa", "v1.0.0", "asset.xz")
	blocked := filepath.Join(cacheDir, "aaa")
	stale := writeCacheFile(t, cacheDir, "distro", "v"+testVersion, testFilename)
	age(t, stale)
	assert.NilError(t, os.Chmod(blocked, 0))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("this user can read a directory with no permissions")
	}

	assert.NilError(t, stager.Prune())

	assertMissing(t, stale)
}

// removeOnLog deletes path the first time the stager logs, which falls between
// Stage's checksum of the cache entry and its copy of that entry.
type removeOnLog struct {
	path string
	done bool
}

func (w *removeOnLog) Write(p []byte) (int, error) {
	if !w.done {
		w.done = true
		_ = os.Remove(w.path)
	}
	return len(p), nil
}

// An entry a concurrent prune takes between the checksum and the copy is a
// cache miss, not a failed build. touch cannot close that window, because the
// prune decides from a stat taken before it.
func TestStageRecoversFromAVanishedCacheEntry(t *testing.T) {
	body := []byte("distro image")
	server := newAssetServer(t, body)
	dep := server.dependency(body)
	stager, cacheDir, destPath := stagerFor(t, nil)

	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))
	assert.NilError(t, os.Remove(destPath))

	stager.Log = &removeOnLog{path: cachedAsset(cacheDir)}
	assert.NilError(t, stager.Stage(t.Context(), dep, destPath))

	assertContent(t, destPath, body)
	assert.Equal(t, server.requests.Load(), int32(2), "the vanished entry must be downloaded again")
}

// rddepman writes some versions with a leading "v". Without normalising it the
// entry would cache under vv1.2.3 and no prune would ever descend to it.
func TestPruneCollectsAVPrefixedVersion(t *testing.T) {
	stager, cacheDir, _ := stagerFor(t, nil)
	dep := Dependency{
		Name:    "wix",
		Version: "v3.14.1",
		Asset:   Asset{Platform: "linux", URL: "https://example.test/wix.tar.gz", Checksum: "sha256:" + strings.Repeat("0", 64)},
	}
	cachePath, err := stager.cachePath(dep)
	assert.NilError(t, err)
	assert.Equal(t, cachePath, filepath.Join(cacheDir, "wix", "v3.14.1", "wix.tar.gz"))

	assert.NilError(t, os.MkdirAll(filepath.Dir(cachePath), 0o755))
	assert.NilError(t, os.WriteFile(cachePath, []byte("archive"), 0o644))
	age(t, cachePath)

	assert.NilError(t, stager.Prune())
	assertMissing(t, cachePath)
}
