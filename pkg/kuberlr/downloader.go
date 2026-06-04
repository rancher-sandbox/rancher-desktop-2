// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package kuberlr

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/version"
)

// defaultKubeMirror is the canonical Kubernetes release CDN (SIG Release).
const defaultKubeMirror = "https://dl.k8s.io"

// envKubeMirror points the resolver at an alternate mirror — offline
// mirrors, and the BATS test's local fake server.
const envKubeMirror = "RDD_KUBECTL_MIRROR"

// downloadTimeout caps each mirror request. Five minutes turns a hung
// mirror into a bounded failure and still covers a ~50 MB kubectl
// binary on slow links (~170 kB/s).
const downloadTimeout = 5 * time.Minute

// httpClient enforces downloadTimeout on every request; http.DefaultClient
// and the cobra context impose none. A shorter deadline on the request
// context still wins.
var httpClient = &http.Client{Timeout: downloadTimeout}

// userAgent names rdd-kuberlr traffic so proxies and air-gapped mirrors
// don't rate-limit or denylist the default Go client UA.
var userAgent = "rdd-kuberlr/" + version.Version

// mirrorURL returns the mirror base URL the resolver downloads from.
//
// TODO(offline): pair this with a "may we download?" toggle so air-gapped
// users can pre-populate CacheDir() and forbid network fetches.
func mirrorURL() string {
	if v := os.Getenv(envKubeMirror); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultKubeMirror
}

// ensureCached returns the path to a cached kubectl matching want. If no
// matching binary exists on disk, ensureCached downloads one from the
// upstream mirror and verifies its sha512 before publishing it atomically.
func ensureCached(ctx context.Context, want semver.Version) (string, error) {
	path := cachedPath(want)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	// User-facing progress for a potentially multi-megabyte fetch. Printed
	// to stderr rather than logged so it shows at the default log level,
	// which is warn outside developer mode.
	fmt.Fprintf(os.Stderr, "Downloading kubectl v%d.%d.%d from %s ...\n", want.Major, want.Minor, want.Patch, mirrorURL())
	if err := download(ctx, want, path); err != nil {
		return "", err
	}
	return path, nil
}

// cachedPath returns the absolute path of the cached kubectl for v.
func cachedPath(v semver.Version) string {
	name := fmt.Sprintf("kubectl-v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(CacheDir(), name)
}

// download fetches kubectl v from the upstream mirror, verifies its sha512
// against the mirror's published checksum, and renames the verified file to
// dst. A failure leaves no partial file behind for the next call to mistake
// for a valid binary.
func download(ctx context.Context, v semver.Version, dst string) error {
	base := fmt.Sprintf("%s/release/v%d.%d.%d/bin/%s/%s/kubectl", mirrorURL(), v.Major, v.Minor, v.Patch, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		base += ".exe"
	}
	want, err := fetchSha512(ctx, base+".sha512")
	if err != nil {
		return fmt.Errorf("fetching checksum: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".kubectl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha512.New()
	if err := streamGet(ctx, base, io.MultiWriter(tmp, h), maxKubectlBytes); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s: %w", base, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: want %s, got %s", base, want, got)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// Body-size caps for streamGet. maxSha512Bytes covers the longest
// sha512sum-format line; maxKubectlBytes covers the binary with headroom
// (~50 MB today). A mirror that serves the binary at the .sha512 URL hits
// the small cap and fails clearly instead of buffering megabytes.
//
// If kubectl ever exceeds maxKubectlBytes, io.LimitReader truncates and
// the digest covers a partial body, so the caller sees "checksum
// mismatch", not a size error. Bump the cap then.
const (
	maxSha512Bytes  = 4 << 10   // 4 KiB
	maxKubectlBytes = 256 << 20 // 256 MiB
)

// fetchSha512 downloads the sha512 hex digest at url. dl.k8s.io serves a
// bare digest; a sha512sum-style mirror appends two spaces and a filename,
// which Fields drops. Rejecting non-128-hex tokens surfaces a "malformed
// checksum" error rather than the misleading "checksum mismatch" a bad
// digest would otherwise produce.
func fetchSha512(ctx context.Context, url string) (string, error) {
	var sb strings.Builder
	if err := streamGet(ctx, url, &sb, maxSha512Bytes); err != nil {
		return "", err
	}
	fields := strings.Fields(sb.String())
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum response from %s", url)
	}
	// Normalize to lowercase to match download's hex.EncodeToString
	// output. PowerShell Get-FileHash emits uppercase hex; without
	// this, the comparison rejects valid digests as mismatched.
	digest := strings.ToLower(fields[0])
	if len(digest) != sha512.Size*2 {
		return "", fmt.Errorf("malformed checksum from %s: %d chars, want %d", url, len(digest), sha512.Size*2)
	}
	for _, c := range digest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", fmt.Errorf("malformed checksum from %s: non-hex character %q", url, c)
		}
	}
	return digest, nil
}

// streamGet performs a GET on url and copies the response body into w,
// capped at maxBytes. The cap turns a malicious or misconfigured mirror
// into a bounded failure: the underlying io.LimitReader stops reading
// after maxBytes regardless of the server's intent. streamGet returns
// an error on any non-200 status.
func streamGet(ctx context.Context, url string, w io.Writer, maxBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	_, err = io.Copy(w, io.LimitReader(resp.Body, maxBytes))
	return err
}
