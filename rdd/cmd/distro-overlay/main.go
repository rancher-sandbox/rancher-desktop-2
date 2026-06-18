// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command distro-overlay merges Rancher Desktop assets into a pristine openSUSE
// distro image or tarball, taking destination paths and ownership from a manifest.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/overlay"
)

func main() {
	manifest := flag.String("manifest", "", "overlay manifest (YAML)")
	source := flag.String("source", "", "directory holding file sources (default: manifest directory)")
	format := flag.String("format", "auto", "distro format: auto, raw, or tar")
	output := flag.String("output", "", "output path for tar format (default: overwrite input)")
	mtimeArg := flag.String("mtime", "", "modification time for every entry, as Unix epoch seconds or RFC3339 (default: now)")
	flag.Parse()

	if *manifest == "" || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: distro-overlay --manifest M [--source D] [--format auto|raw|tar] [--output O] [--mtime T] <distro>")
		os.Exit(2)
	}
	mtime, err := parseMtime(*mtimeArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "distro-overlay:", err)
		os.Exit(2)
	}
	if err := run(*manifest, *source, *format, *output, flag.Arg(0), mtime); err != nil {
		fmt.Fprintln(os.Stderr, "distro-overlay:", err)
		os.Exit(1)
	}
}

// parseMtime reads the --mtime flag as Unix epoch seconds or an RFC3339
// timestamp, defaulting to the current time when the flag is empty. A build
// passes a deterministic value, such as a git commit time, for a reproducible
// result.
func parseMtime(arg string) (time.Time, error) {
	if arg == "" {
		return time.Now(), nil
	}
	if secs, err := strconv.ParseInt(arg, 10, 64); err == nil {
		return time.Unix(secs, 0).UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, arg); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --mtime %q: want Unix epoch seconds or RFC3339", arg)
}

func run(manifestPath, sourceDir, format, output, distro string, mtime time.Time) error {
	m, err := overlay.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	if sourceDir == "" {
		sourceDir = filepath.Dir(manifestPath)
	}
	if format == "auto" {
		if format, err = detectFormat(distro); err != nil {
			return err
		}
	}
	switch format {
	case "raw":
		d, err := overlay.OpenImage(distro)
		if err != nil {
			return err
		}
		return overlay.Apply(d, m, sourceDir, mtime)
	case "tar":
		return applyTarFile(distro, output, m, sourceDir, mtime)
	default:
		return fmt.Errorf("unknown format %q", format)
	}
}

// detectFormat distinguishes an OEM disk image from a tarball by signature.
func detectFormat(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if sig := make([]byte, 8); readAt(f, sig, 512) && string(sig) == "EFI PART" {
		return "raw", nil // GPT header at LBA 1
	}
	if magic := make([]byte, 5); readAt(f, magic, 257) && string(magic) == "ustar" {
		return "tar", nil // POSIX tar magic
	}
	return "", fmt.Errorf("cannot detect distro format of %s; pass --format", p)
}

func readAt(f *os.File, b []byte, off int64) bool {
	_, err := f.ReadAt(b, off)
	return err == nil
}

// applyTarFile overlays a tarball, overwriting the input when output is empty.
func applyTarFile(input, output string, m *overlay.Manifest, sourceDir string, mtime time.Time) error {
	in, err := os.Open(input)
	if err != nil {
		return err
	}
	defer in.Close()

	dst := output
	inPlace := output == ""
	if inPlace {
		dst = input + ".tmp"
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := overlay.ApplyTar(in, out, m, sourceDir, mtime); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if inPlace {
		_ = in.Close()
		return os.Rename(dst, input)
	}
	return nil
}
