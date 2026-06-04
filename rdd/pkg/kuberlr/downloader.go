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

// downloadTimeout bounds a hung mirror request while still covering a
// ~50 MB kubectl download on slow links (~170 kB/s).
const downloadTimeout = 5 * time.Minute

// httpClient enforces downloadTimeout, since http.DefaultClient and the
// cobra context impose none (a shorter request-context deadline still wins).
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

// latestPatch returns the latest patch release of the server's minor,
// read from the mirror's stable-<major>.<minor>.txt version marker
// (e.g. stable-1.34.txt → v1.34.9). The release CDN publishes markers
// only for GA minors, so a pre-GA server version fails here with a 404.
func latestPatch(ctx context.Context, server semver.Version) (semver.Version, error) {
	url := fmt.Sprintf("%s/release/stable-%d.%d.txt", mirrorURL(), server.Major, server.Minor)
	var sb strings.Builder
	if err := streamGet(ctx, url, &sb, maxTextBytes); err != nil {
		return semver.Version{}, err
	}
	v, err := semver.ParseTolerant(strings.TrimSpace(sb.String()))
	if err != nil {
		return semver.Version{}, fmt.Errorf("malformed version marker from %s: %w", url, err)
	}
	if v.Major != server.Major || v.Minor != server.Minor {
		return semver.Version{}, fmt.Errorf("version marker %s names v%s, not a v%d.%d release", url, v, server.Major, server.Minor)
	}
	return v, nil
}

// ensureCached returns the cached kubectl path for want, downloading
// and sha512-verifying it first if the cache misses.
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
	// User-facing progress, printed to stderr (not logged) so it shows at
	// the default log level (warn outside developer mode).
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

// download fetches kubectl v, sha512-verifies it, and renames it to
// dst atomically — a failure leaves no partial file behind.
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

// Body-size caps for streamGet: maxTextBytes covers a one-line text
// response (sha512sum line, version marker); maxKubectlBytes covers
// the binary with headroom. A truncated binary surfaces as "checksum
// mismatch", not a size error — bump the cap if real kubectl exceeds it.
const (
	maxTextBytes    = 4 << 10   // 4 KiB
	maxKubectlBytes = 256 << 20 // 256 MiB
)

// fetchSha512 downloads the sha512 hex digest at url (bare, or
// sha512sum-style with a trailing filename Fields drops). Rejecting
// non-hex tokens up front gives "malformed checksum", not a misleading
// "checksum mismatch".
func fetchSha512(ctx context.Context, url string) (string, error) {
	var sb strings.Builder
	if err := streamGet(ctx, url, &sb, maxTextBytes); err != nil {
		return "", err
	}
	fields := strings.Fields(sb.String())
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum response from %s", url)
	}
	// Lowercase to match hex.EncodeToString's output — PowerShell's
	// Get-FileHash emits uppercase, which would otherwise look mismatched.
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

// streamGet GETs url into w, capped at maxBytes so a malicious or
// misconfigured mirror can't stream unbounded data; non-200 is an error.
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
