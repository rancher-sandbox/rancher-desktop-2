// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

// This file rewrites the host paths in nerdctl arguments to the /mnt/<drive>
// locations where the WSL2 guest automounts them. cmd/rdd calls it only on
// Windows.

import (
	"encoding/csv"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tonistiigi/go-csvvalue"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/guestexec"
)

// namedVolume matches the volume sources that nerdctl takes for volume names
// instead of host paths; it copies isNamedVolume in nerdctl's pkg/mountutil.
var namedVolume = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)

// windowsDrive matches a leading Windows drive, as in `C:\data` or the
// drive-relative `d:app`. A one-letter source cannot be a volume name.
var windowsDrive = regexp.MustCompile(`^[a-zA-Z]:`)

// driveCurrentDir matches the current directory of a drive, `d:` or `d:.`.
var driveCurrentDir = regexp.MustCompile(`^[a-zA-Z]:\.?$`)

// volumeArgHandler handles the argument of `nerdctl run --volume=...`.
func volumeArgHandler(arg string) (string, error) {
	// Valid arguments are:
	// <container path>, an anonymous volume
	// <host path or volume name>:<container path>
	// <host path or volume name>:<container path>:<mode>
	// The mode passes through verbatim for nerdctl to interpret.
	hostPath, rest, found := guestexec.CutVolumeSource(arg)
	if !found {
		if windowsDrive.MatchString(hostPath) {
			// nerdctl splits at the drive's colon, so `C:/data` would create a
			// directory named `C` and mount it at /data.
			return "", fmt.Errorf("invalid volume mount %s: expected source:container-path", arg)
		}
		// An anonymous volume.
		return arg, nil
	}
	if namedVolume.MatchString(hostPath) {
		return arg, nil
	}
	guestPath, err := guestexec.TranslateHostPath(hostPath)
	if err != nil {
		return "", fmt.Errorf("could not get volume host path for %s: %w", arg, err)
	}
	return guestPath + ":" + rest, nil
}

// mountArgHandler handles the argument of `nerdctl run --mount=...`.
func mountArgHandler(arg string) (string, error) {
	// Like nerdctl's ProcessFlagMount, match keys in any case and values
	// exactly. The mount type defaults to volume, and the last `type=bind`
	// or `type=tmpfs` sets it; `type=volume` leaves it as it was. Volume and
	// bind mounts read a nonempty source that is not a volume name as a host
	// path, and tmpfs mounts ignore the source.
	parts := strings.Split(arg, ",")
	isTmpfs := false
	for _, part := range parts {
		key, value, _ := strings.Cut(part, "=")
		if !strings.EqualFold(key, "type") {
			continue
		}
		switch value {
		case "bind":
			isTmpfs = false
		case "tmpfs":
			isTmpfs = true
		}
	}
	if isTmpfs {
		return arg, nil
	}
	for i, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if k := strings.ToLower(key); k != "source" && k != "src" {
			continue
		}
		// nerdctl joins the source to the target with a colon and splits the
		// result like a `--volume` spec.
		source, rest, found := guestexec.CutVolumeSource(value)
		if namedVolume.MatchString(source) {
			continue
		}
		guestPath, err := filePathArgHandler(source)
		if err != nil {
			return "", err
		}
		if found {
			guestPath += ":" + rest
		}
		parts[i] = key + "=" + guestPath
	}
	return strings.Join(parts, ","), nil
}

// filePathArgHandler handles arguments that take a path for input.
func filePathArgHandler(arg string) (string, error) {
	if arg == "" {
		// An empty value does not name a path. nerdctl takes most empty
		// flags as unset and resolves other empty values in its own working
		// directory, the guest's view of the host one.
		return arg, nil
	}
	guestPath, err := guestexec.TranslateHostPath(arg)
	if err != nil {
		return "", err
	}
	return keepDirSuffix(arg, guestPath), nil
}

// keepDirSuffix keeps guestPath in directory form when the host path arg is
// `.`, ends in `/` or `/.` with either slash, or names the current directory
// of a drive. TranslateHostPath cleans that form away, but nerdctl reads
// `build --file sub/` and `build --file .` as the Dockerfile in that
// directory.
func keepDirSuffix(arg, guestPath string) string {
	slashed := strings.ReplaceAll(arg, `\`, "/")
	suffix := ""
	switch {
	case slashed == "." || strings.HasSuffix(slashed, "/.") || driveCurrentDir.MatchString(slashed):
		suffix = "/."
	case strings.HasSuffix(slashed, "/"):
		suffix = "/"
	}
	switch {
	case strings.HasSuffix(guestPath, suffix):
		return guestPath
	case strings.HasSuffix(guestPath, "/"):
		// A drive root such as /mnt/d/ keeps its slash.
		return guestPath + strings.TrimPrefix(suffix, "/")
	}
	return guestPath + suffix
}

// filePathListArgHandler handles a comma-separated list of input paths. It
// splits and joins the list as one CSV record, the way pflag's StringSlice
// reads and writes it.
func filePathListArgHandler(arg string) (string, error) {
	if arg == "" {
		return arg, nil
	}
	paths, err := csv.NewReader(strings.NewReader(arg)).Read()
	if err != nil {
		// nerdctl rejects the value too, and reports it.
		return arg, nil
	}
	for i, p := range paths {
		if paths[i], err = filePathArgHandler(p); err != nil {
			return "", err
		}
	}
	result, err := writeCSVRecord(paths)
	if err != nil {
		return "", err
	}
	if reread, err := csv.NewReader(strings.NewReader(result)).Read(); err != nil || !slices.Equal(reread, paths) {
		// csv.Writer writes a lone empty entry as an empty value, which pflag
		// reads as no entries. csv.Reader reads CR LF inside an entry back as
		// LF. Either way nerdctl would read a different list, so report the
		// value here.
		return "", fmt.Errorf("invalid path list %q: it is a single empty entry, or an entry contains CR LF", arg)
	}
	return result, nil
}

// writeCSVRecord joins fields as one CSV record, quoting the fields that need
// it.
func writeCSVRecord(fields []string) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write(fields); err != nil {
		return "", err
	}
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// outputPathArgHandler handles arguments that take a path to write to. On
// Windows the guest writes through the /mnt/<drive> mount directly, so this
// matches the input handling.
func outputPathArgHandler(arg string) (string, error) {
	return filePathArgHandler(arg)
}

// urlLikeRE matches a value that starts with a URI scheme, such as `https:`
// or `pkcs11:`. A scheme has at least two characters, so a drive path such as
// `C://dir` does not match.
var urlLikeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]+:`)

// fileOrURLOrStdinArgHandler rewrites a file path but passes `-` and URIs
// through, for arguments such as `builder build --file`, where `-` is stdin.
func fileOrURLOrStdinArgHandler(arg string) (string, error) {
	if arg == "-" || urlLikeRE.MatchString(arg) {
		return arg, nil
	}
	return filePathArgHandler(arg)
}

// streamsToStdout reports whether the builder CSV fields export a single
// file, for which `dest=-` means stdout instead of a file named `-`. It
// follows buildctl's resolveExporterDest. The tar exporter always writes a
// file, and the oci and docker exporters do unless the last `tar=` value
// parses as false.
func streamsToStdout(parts []string) bool {
	exporter, tar := "", ""
	for _, part := range parts {
		key, value, _ := strings.Cut(part, "=")
		switch strings.ToLower(key) {
		case "type":
			exporter = value
		case "tar":
			tar = value
		}
	}
	switch exporter {
	case "tar":
		return true
	case "oci", "docker":
		isTar, err := strconv.ParseBool(tar)
		return err != nil || isTar
	}
	return false
}

// builderCacheArgHandler handles the builder CSV values that buildctl
// reads, such as `--cache-to=`, `--output=` with a `type=`, and `--secret=`.
func builderCacheArgHandler(arg string) (string, error) {
	// The arg is CSV, with `src=` and `source=` values as inputs and `dest=`
	// values as outputs; everything else passes through. Parse it with
	// buildctl's own CSV parser, and match keys in any case as buildctl does.
	parts, err := csvvalue.Fields(arg, nil)
	if err != nil {
		// An empty value does not name a path, and buildctl reports a
		// malformed one itself.
		return arg, nil
	}
	secretType, envName := "", ""
	for _, part := range parts {
		key, value, _ := strings.Cut(part, "=")
		switch strings.ToLower(key) {
		case "type":
			secretType = value
		case "env":
			envName = value
		}
	}
	// buildctl reads the source of a secret whose last `type=` is env as the
	// name of an environment variable, unless the last `env=` names one.
	envSecret := secretType == "env" && envName == ""
	changed := false
	for i, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		var handler argHandler
		switch strings.ToLower(key) {
		case "src", "source":
			if envSecret {
				continue
			}
			handler = filePathArgHandler
		case "dest":
			if value == "-" && streamsToStdout(parts) {
				continue
			}
			handler = outputPathArgHandler
		default:
			continue
		}
		fixedPath, err := handler(value)
		if err != nil {
			return "", err
		}
		if fixedPath != value {
			parts[i] = key + "=" + fixedPath
			changed = true
		}
	}
	if !changed {
		return arg, nil
	}
	return writeCSVRecord(parts)
}

// builderOutputArgHandler handles `nerdctl builder build --output=`.
func builderOutputArgHandler(arg string) (string, error) {
	if strings.Contains(arg, "type=") {
		return builderCacheArgHandler(arg)
	}
	// nerdctl passes a value without `type=` on as `type=local,dest=<value>`,
	// and buildctl parses that as CSV, so a comma in the value starts another
	// field that buildctl reads too.
	const wrapper = "type=local,dest="
	result, err := builderCacheArgHandler(wrapper + arg)
	if err != nil {
		return "", err
	}
	// The wrapper stays when the guest path needs CSV quotes, which must
	// open before `dest=`, or when a translated path brings `type=` in from
	// a working directory, which keeps nerdctl from adding the wrapper.
	trimmed, ok := strings.CutPrefix(result, wrapper)
	if !ok || strings.Contains(trimmed, "type=") {
		return result, nil
	}
	return trimmed, nil
}

// sshArgHandler handles `nerdctl builder build --ssh=` and `builder debug
// --ssh=`, which take `id=path,path,...` like buildctl. buildctl v0.23.0 and
// later read an element `raw=<bool>` as an option instead of a path, so it
// passes through.
func sshArgHandler(arg string) (string, error) {
	id, value, ok := strings.Cut(arg, "=")
	if !ok {
		// A bare ID such as `default` does not name a path.
		return arg, nil
	}
	paths := strings.Split(value, ",")
	for i, p := range paths {
		// buildkit takes a lone empty path like a bare ID.
		if p == "" || strings.HasPrefix(p, "raw=") {
			continue
		}
		guestPath, err := guestexec.TranslateHostPath(p)
		if err != nil {
			return "", err
		}
		paths[i] = guestPath
	}
	return id + "=" + strings.Join(paths, ","), nil
}

// buildContextArgHandler handles `nerdctl builder build --build-context=`.
func buildContextArgHandler(arg string) (string, error) {
	// Like nerdctl's parseContextNames, split once at `=`. A value with a
	// URL, image, or target prefix passes through; any other value is a
	// path, after an optional oci-layout:// prefix.
	name, value, ok := strings.Cut(arg, "=")
	if !ok {
		// nerdctl rejects the value too, and reports it.
		return arg, nil
	}
	passThroughPrefixes := []string{"https://", "http://", "docker-image://", "target:"}
	matchesPrefix := func(prefix string) bool {
		return strings.HasPrefix(value, prefix)
	}
	if slices.ContainsFunc(passThroughPrefixes, matchesPrefix) {
		return arg, nil
	}
	layoutPrefix := ""
	if path, ok := strings.CutPrefix(value, "oci-layout://"); ok {
		layoutPrefix, value = "oci-layout://", path
	}
	guestPath, err := guestexec.TranslateHostPath(value)
	if err != nil {
		return "", err
	}
	return name + "=" + layoutPrefix + guestPath, nil
}
