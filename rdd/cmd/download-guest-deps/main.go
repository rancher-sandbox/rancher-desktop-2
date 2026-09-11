// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command download-guest-deps stages the guest dependencies the rdd build bakes
// into its binaries.
//
// The target defaults to the host. A build that cross-compiles has to pass
// --os and --arch; CI does cross-compile, packaging the macOS x86_64 build on
// an arm64 runner with GOARCH set for `make build-rdd`. They are flags rather
// than the GOOS and GOARCH environment variables, which would cross-compile
// this tool too and leave nothing that can run.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/guestdeps"
)

func main() {
	manifest := flag.String("manifest", "dependencies.yaml", "guest dependency manifest (YAML)")
	dest := flag.String("dest", filepath.Join("pkg", "embedded"), "directory to stage the assets into")
	cache := flag.String("cache", "", "directory the verified downloads are kept and pruned in (default: under the user cache directory)")
	goos := flag.String("os", runtime.GOOS, "the operating system the build targets")
	goarch := flag.String("arch", runtime.GOARCH, "the architecture the build targets")
	flag.Parse()

	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: download-guest-deps [--manifest M] [--dest D] [--cache C] [--os OS] [--arch ARCH]")
		os.Exit(2)
	}
	// A download runs for minutes; let an interrupt abort it and clean up.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Stderr, *manifest, *dest, *cache, *goos, *goarch)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "download-guest-deps:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log io.Writer, manifestPath, destDir, cacheDir, goos, goarch string) error {
	manifest, err := guestdeps.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	if cacheDir == "" {
		if cacheDir, err = guestdeps.DefaultCacheDir(); err != nil {
			return err
		}
	}
	stager := &guestdeps.Stager{CacheDir: cacheDir, Log: log}
	for _, staged := range stagedDeps(goos, goarch) {
		dep, err := manifest.Select(staged.name, staged.selector)
		if err != nil {
			return fmt.Errorf("%s: %w", manifestPath, err)
		}
		if err := stager.Stage(ctx, dep, filepath.Join(destDir, staged.filename)); err != nil {
			return err
		}
	}
	// Everything the build needs is staged by now, so a cache that cannot be
	// pruned is a nuisance rather than a failure.
	if err := stager.Prune(); err != nil {
		fmt.Fprintln(log, "warning: pruning the download cache:", err)
	}
	return nil
}

// A stagedDep is one guest dependency the build needs on disk, under the file
// name the build expects it at.
type stagedDep struct {
	name     string
	selector guestdeps.Selector
	filename string
}

// stagedDeps lists what a build for goos/goarch stages. Guest dependencies run
// in the Linux VM, so every asset is a linux one whatever the host; only the
// image format follows the host, because WSL2 imports the rootfs tarball while
// Lima's vz and qemu drivers boot the raw ext4 image.
func stagedDeps(goos, goarch string) []stagedDep {
	variant := "raw"
	if goos == "windows" {
		variant = "tar"
	}
	return []stagedDep{{
		name:     "distro",
		selector: guestdeps.Selector{Platform: "linux", Arch: goarch, Variant: variant},
		filename: "distro." + variant + ".xz",
	}}
}
