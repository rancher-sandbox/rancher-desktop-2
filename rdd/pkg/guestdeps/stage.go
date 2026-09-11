// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package guestdeps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// downloadAttempts bounds the retries of a transfer that drops, stalls or is
// throttled partway through a several-hundred-megabyte asset. Anything longer
// is an outage a re-run has to cover.
const downloadAttempts = 4

// stallTimeout is the longest a transfer may go without progress, waiting
// either for the response headers or for the next bytes of the body. Tests
// shorten it; newHTTPClient reads it, so that moves the body deadline alone
// until httpClient is rebuilt.
var stallTimeout = 30 * time.Second

// downloadTimeout bounds every attempt at one asset together. stallTimeout ends
// a peer that goes quiet; this ends one that keeps sending just enough to
// restart that timer, which could otherwise hold the build open indefinitely.
// No working link reaches it. The largest asset the manifest names is 250 MiB,
// which needs only 36 KiB/s to arrive in time. Tests shorten it.
var downloadTimeout = 2 * time.Hour

// progressInterval is how often a running download reports how far it has got.
// A quiet minute on a 200 MiB asset looks the same as a wedged one.
const progressInterval = 5 * time.Second

// cacheTTL is how long a download nothing has used is kept. Every build touches
// the entries it stages from, so what goes untouched this long is a superseded
// version or the partial file a killed build left behind.
const cacheTTL = 7 * 24 * time.Hour

// retryDelay is the wait before the second attempt, and maxRetryDelay the
// ceiling retryWait may grow it to. They are variables so tests can shorten
// them.
var (
	retryDelay    = 2 * time.Second
	maxRetryDelay = 30 * time.Second
)

// httpClient bounds what http.DefaultClient leaves open.
var httpClient = newHTTPClient()

// newHTTPClient returns a client that bounds the wait for response headers.
// DefaultTransport times out the dial and the TLS handshake only, so that wait
// needs a limit of its own; progressReader bounds the body read.
func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = stallTimeout
	return &http.Client{Transport: transport}
}

// A Stager fetches guest dependencies into CacheDir and copies them into the
// build tree. Progress goes to Log, or nowhere when Log is nil.
type Stager struct {
	CacheDir string
	Log      io.Writer
}

// DefaultCacheDir returns the directory holding verified downloads. It is
// outside the source tree, so a clean checkout and each bats worktree share one
// copy of an asset. Prune deletes underneath it, so it needs a directory of its
// own, clear of the app's cache on macOS and Linux and of every instance
// directory on Windows, where os.UserCacheDir is %LocalAppData% and rdd svc
// delete removes the instance whole. The dot keeps the name out of reach of
// instance.Name(), which is always "rancher-desktop-" and a suffix.
func DefaultCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating the download cache: %w", err)
	}
	return filepath.Join(dir, "rancher-desktop.guest-deps"), nil
}

// Stage makes destPath hold dep's verified bytes, downloading them first unless
// the cache already has them. It checksums both destPath and the cache entry
// rather than trusting a timestamp, so a half-written file from an interrupted
// build is replaced.
func (s *Stager) Stage(ctx context.Context, dep Dependency, destPath string) error {
	cachePath, err := s.cachePath(dep)
	if err != nil {
		return err
	}
	// Reaching for the cache before destPath would re-fetch a purged entry to
	// produce a file the build already has, and would fail offline where the
	// whole download is avoidable.
	staged, err := fileHasChecksum(destPath, dep.Asset.Checksum)
	if err != nil {
		return err
	}
	if staged {
		s.logf("%s is up to date", destPath)
		s.touch(cachePath)
		return nil
	}

	cached, err := fileHasChecksum(cachePath, dep.Asset.Checksum)
	if err != nil {
		return err
	}
	if cached {
		s.logf("%s %s is already downloaded to %s", dep.Name, dep.Version, cachePath)
		s.logf("Copying %s to %s", cachePath, destPath)
		s.touch(cachePath)
		// touch narrows the window but cannot close it: a concurrent prune
		// decides from a stat taken before it, and unlink succeeds whatever the
		// timestamp says. An entry that goes missing here is a cache miss, but
		// copyFile reports a vanished destination directory the same way, so
		// check which file is gone.
		err := copyFile(cachePath, destPath)
		if !errors.Is(err, fs.ErrNotExist) || !missing(cachePath) {
			return err
		}
		s.logf("%s went away before it could be copied; downloading it again", cachePath)
	}
	if err := s.download(ctx, dep, cachePath); err != nil {
		return err
	}
	s.logf("Copying %s to %s", cachePath, destPath)
	return copyFile(cachePath, destPath)
}

// touch records that a build used a cache entry, so Prune keeps it for another
// cacheTTL. A missing entry is not an error, because a build whose staged copy
// is already correct never opens one. Nor does touch verify the entry, because
// hashing it would cost what that staged copy exists to avoid; a corrupt entry
// outlives its TTL and is replaced the first time a build needs it.
func (s *Stager) touch(cachePath string) {
	now := time.Now()
	if err := os.Chtimes(cachePath, now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logf("Could not refresh %s: %v", cachePath, err)
	}
}

// Prune removes downloads no build has used in cacheTTL, and with them the
// partial files a build killed mid-transfer leaves behind. It descends only
// directories named as isVersionDir describes, and removes nothing else: the
// dependency directory stays, because an empty one is indistinguishable from a
// directory this tool never made, and one per dependency costs nothing.
//
// Pruning is best effort. Another build sweeping the same cache removes
// directories underfoot, so an entry that cannot be read is logged and skipped
// rather than abandoning the rest of the sweep.
func (s *Stager) Prune() error {
	cutoff := time.Now().Add(-cacheTTL)
	deps, err := os.ReadDir(s.CacheDir)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing has been downloaded yet.
		return nil
	}
	if err != nil {
		return err
	}
	for _, dep := range deps {
		if !dep.IsDir() {
			continue
		}
		depDir := filepath.Join(s.CacheDir, dep.Name())
		versions, err := os.ReadDir(depDir)
		if err != nil {
			s.skipped(depDir, err)
			continue
		}
		for _, version := range versions {
			if version.IsDir() && isVersionDir(version.Name()) {
				s.pruneVersion(filepath.Join(depDir, version.Name()), cutoff)
			}
		}
	}
	return nil
}

// isVersionDir reports whether name is one of the directories cachePath names
// after a dependency's version. Requiring a digit after the "v" keeps an
// unrelated directory, a vendor directory say, out of the prune. A version that
// does not start with a digit is never collected, which wastes disk rather than
// deleting something this tool does not own.
func isVersionDir(name string) bool {
	return len(name) > 1 && name[0] == 'v' && name[1] >= '0' && name[1] <= '9'
}

// pruneVersion removes the downloads in one version directory that no build has
// used since cutoff.
func (s *Stager) pruneVersion(dir string, cutoff time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.skipped(dir, err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			s.skipped(filepath.Join(dir, entry.Name()), err)
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		name := filepath.Join(dir, entry.Name())
		if err := os.Remove(name); err != nil {
			s.skipped(name, err)
			continue
		}
		s.logf("Removed the unused download %s", name)
	}
	removeStaleEmptyDir(dir, cutoff)
}

// skipped reports what a prune passed over. A path another build removed first
// is the expected case and says nothing worth printing.
func (s *Stager) skipped(path string, err error) {
	if !errors.Is(err, fs.ErrNotExist) {
		s.logf("Skipped %s: %v", path, err)
	}
}

// removeStaleEmptyDir drops a directory holding nothing that nothing has
// written to since cutoff. An empty directory's timestamp is the moment it was
// created, so the one a concurrent build has just made for its temporary file
// stays; a directory this run emptied is likewise too new, and goes on a later
// run. Failing to remove one costs nothing, because it stays a candidate.
func removeStaleEmptyDir(dir string, cutoff time.Time) {
	info, err := os.Stat(dir)
	if err != nil || info.ModTime().After(cutoff) {
		return
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) > 0 {
		return
	}
	_ = os.Remove(dir)
}

// cachePath returns where dep's download is kept, keyed by name, version and
// file name, so a version bump goes beside the old download instead of
// overwriting it. rddepman writes some versions with a leading "v" and some
// without, and the prefix is normalised here so both land in a directory
// isVersionDir recognises.
func (s *Stager) cachePath(dep Dependency) (string, error) {
	u, err := url.Parse(dep.Asset.URL)
	if err != nil {
		return "", fmt.Errorf("%s %s has an unparseable url %q: %w", dep.Name, dep.Asset, dep.Asset.URL, err)
	}
	version := "v" + strings.TrimPrefix(dep.Version, "v")
	return filepath.Join(s.CacheDir, dep.Name, version, path.Base(u.Path)), nil
}

func (s *Stager) logf(format string, args ...any) {
	if s.Log == nil {
		return
	}
	fmt.Fprintf(s.Log, format+"\n", args...)
}

// fatalError marks a download failure no retry can fix, such as a URL the
// server does not have or bytes that will never match the checksum.
type fatalError struct{ err error }

func (e *fatalError) Error() string { return e.err.Error() }

func (e *fatalError) Unwrap() error { return e.err }

// retryAfterError holds the delay a throttling server named, so the retry waits
// that long instead of following its own schedule.
type retryAfterError struct {
	err   error
	delay time.Duration
}

func (e *retryAfterError) Error() string { return e.err.Error() }

func (e *retryAfterError) Unwrap() error { return e.err }

// retryWait returns the wait before attempt, either the delay the server asked
// for or an exponential backoff. Both stop at maxRetryDelay, so a server naming
// an hour cannot park the build there.
func retryWait(attempt int, err error) time.Duration {
	var throttled *retryAfterError
	if errors.As(err, &throttled) {
		return min(throttled.delay, maxRetryDelay)
	}
	return min(retryDelay<<(attempt-1), maxRetryDelay)
}

// maxRetryAfterSeconds is the largest Retry-After a time.Duration can hold.
// Converting anything above it wraps to a negative delay, which would retry at
// once rather than waiting.
const maxRetryAfterSeconds = math.MaxInt64 / int64(time.Second)

// retryAfter reads a Retry-After header, which holds either a number of seconds
// or an HTTP date. A value that is absent, malformed or already past reports
// false.
func retryAfter(value string, now time.Time) (time.Duration, bool) {
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0, false
		}
		return time.Duration(min(int64(seconds), maxRetryAfterSeconds)) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	// Sub saturates rather than wrapping, so a far-future date is safe here.
	delay := when.Sub(now)
	return delay, delay > 0
}

// download fetches dep to destPath, retrying a transfer that fails partway.
func (s *Stager) download(ctx context.Context, dep Dependency, destPath string) error {
	// The attempts share one deadline, so the retries cannot multiply it.
	// Keeping the caller's context separate tells a cancelled build from a
	// download that ran out of time.
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	var err error
attempts:
	for attempt := range downloadAttempts {
		if attempt > 0 {
			wait := retryWait(attempt, err)
			s.logf("Retrying in %s (attempt %d of %d): %v", wait, attempt+1, downloadAttempts, err)
			select {
			case <-ctx.Done():
				break attempts
			case <-time.After(wait):
			}
		}
		s.logf("Downloading %s to %s", dep.Asset.URL, destPath)
		start := time.Now()
		var size int64
		if size, err = s.downloadOnce(ctx, dep.Asset, destPath); err == nil {
			s.logf("Downloaded %.1f MiB in %s", mib(size), time.Since(start).Round(time.Second))
			return nil
		}
		var fatal *fatalError
		if errors.As(err, &fatal) || ctx.Err() != nil {
			break
		}
	}
	switch {
	case caller.Err() != nil:
		return fmt.Errorf("downloading %s: %w", dep.Asset.URL, caller.Err())
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("downloading %s: gave up after %s: %w", dep.Asset.URL, downloadTimeout, err)
	}
	return fmt.Errorf("downloading %s: %w", dep.Asset.URL, err)
}

// downloadOnce writes the asset to destPath through a temporary file, hashing
// as it goes, and renames it into place only once the bytes match the
// manifest's checksum.
func (s *Stager) downloadOnce(ctx context.Context, asset Asset, destPath string) (size int64, err error) {
	// The attempt gets its own cancellation, so a stall ends this attempt while
	// leaving download's context untouched for the retry that follows.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, http.NoBody)
	if err != nil {
		return 0, &fatalError{err}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("unexpected status %s", resp.Status)
		// A server naming a delay is asking to be retried, whatever status it sent.
		// GitHub throttles release downloads with 403 as well as 429.
		if delay, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			return 0, &retryAfterError{err: err, delay: delay}
		}
		// Otherwise a 4xx means the manifest names something the server does not
		// have, which a retry cannot conjure up. The two exceptions are about the
		// request itself, so repeating it can succeed.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 &&
			resp.StatusCode != http.StatusTooManyRequests &&
			resp.StatusCode != http.StatusRequestTimeout {
			return 0, &fatalError{err}
		}
		return 0, err
	}

	tmp, err := createTemp(destPath)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	body := s.watchTransfer(resp, cancel)
	defer body.stop()

	hash := sha256.New()
	if size, err = io.Copy(io.MultiWriter(tmp, hash), body); err != nil {
		// A stall reaches the read as a bare cancellation, so name what happened.
		if body.stalled.Load() {
			err = fmt.Errorf("no data received for %s", stallTimeout)
		}
		return size, err
	}
	if sum := "sha256:" + hex.EncodeToString(hash.Sum(nil)); sum != asset.Checksum {
		err = &fatalError{fmt.Errorf("checksum is %s, want %s", sum, asset.Checksum)}
		return size, err
	}
	err = finishTemp(tmp, destPath)
	return size, err
}

// copyFile copies src to dst through a temporary file, so an interrupted copy
// leaves no partial dst behind for the build to embed.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := createTemp(dst)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if _, err = io.Copy(tmp, in); err != nil {
		return err
	}
	return finishTemp(tmp, dst)
}

// createTemp opens a temporary file beside destPath, creating the directory
// that will hold it. A concurrent prune can drop that directory between the two
// calls, so a second round makes it again rather than failing the attempt.
func createTemp(destPath string) (*os.File, error) {
	dir, prefix := filepath.Dir(destPath), filepath.Base(destPath)+".tmp-*"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, prefix)
	if !errors.Is(err, fs.ErrNotExist) {
		return tmp, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, prefix)
}

// finishTemp flushes tmp and renames it to destPath. os.CreateTemp creates the
// file 0o600; the staged asset is a build input others may read, so widen it.
//
// On Windows the rename can fail with a sharing violation while another build
// reads destPath, because Go opens files without FILE_SHARE_DELETE and
// MoveFileEx needs delete access to the file it replaces. Two builds sharing
// one cache is the design, so this is reachable; a re-run clears it.
func finishTemp(tmp *os.File, destPath string) error {
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), destPath)
}

// missing reports whether path does not exist. Any other error leaves the
// question open, which counts as present, so the caller reports the error it
// already has.
func missing(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// fileHasChecksum reports whether path holds exactly the bytes checksum names.
// A missing file is not an error, just not a match.
func fileHasChecksum(path, checksum string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return false, err
	}
	return "sha256:"+hex.EncodeToString(hash.Sum(nil)) == checksum, nil
}

// A progressReader wraps a download's body to report how far a long transfer
// has got and to end one that goes quiet. Every read restarts the stall timer,
// so that limit falls on the gap between bytes; downloadTimeout bounds the
// transfer itself, which can run for minutes.
type progressReader struct {
	body    io.Reader
	total   int64
	read    int64
	report  func(read, total int64)
	timer   *time.Timer
	stalled atomic.Bool
	next    time.Time
}

// watchTransfer starts the stall timer on resp's body. cancel ends the attempt
// when the timer fires, so the retry loop gets a failed attempt rather than a
// wedged one.
func (s *Stager) watchTransfer(resp *http.Response, cancel context.CancelFunc) *progressReader {
	r := &progressReader{
		body:   resp.Body,
		total:  resp.ContentLength,
		report: s.logProgress,
		next:   time.Now().Add(progressInterval),
	}
	r.timer = time.AfterFunc(stallTimeout, func() {
		r.stalled.Store(true)
		cancel()
	})
	return r
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	r.read += int64(n)
	r.timer.Reset(stallTimeout)
	if now := time.Now(); now.After(r.next) {
		r.next = now.Add(progressInterval)
		r.report(r.read, r.total)
	}
	return n, err
}

func (r *progressReader) stop() { r.timer.Stop() }

// logProgress reports how far a transfer has got. A server that sent no
// Content-Length leaves the total unknown, so the report gives the bytes alone.
func (s *Stager) logProgress(read, total int64) {
	if total <= 0 {
		s.logf("Downloaded %.1f MiB", mib(read))
		return
	}
	s.logf("Downloaded %.1f MiB of %.1f MiB (%d%%)", mib(read), mib(total), read*100/total)
}

// mib renders a byte count in MiB.
func mib(bytes int64) float64 { return float64(bytes) / (1 << 20) }
