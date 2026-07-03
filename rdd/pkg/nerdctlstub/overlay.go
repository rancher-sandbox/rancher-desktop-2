// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

// argHandlerKind classifies how a flag value containing host paths is
// rewritten before the arguments reach nerdctl in the guest.
type argHandlerKind int

const (
	// filePathArg is a plain path to an input file or directory.
	filePathArg argHandlerKind = iota
	// outputPathArg is a plain path nerdctl writes to (e.g. --cidfile).
	outputPathArg
	// volumeArg is host-path:container-path[:ro|:rw].
	volumeArg
	// mountArg is CSV such as type=bind,source=host-path,target=....
	mountArg
	// builderCacheArg is CSV with src= (input) and dest= (output) paths.
	builderCacheArg
	// buildContextArg is name=value CSV where value may be a path or a URN.
	buildContextArg
)

// pathFlags classifies every nerdctl flag whose value contains host paths.
// Keys are canonical command paths; aliases resolve before lookup. The
// ./generate tool validates each entry against the pinned nerdctl version
// and fails when a value-consuming flag smells like a path but appears
// neither here nor in notPathFlags, so a nerdctl bump surfaces exactly the
// flags that need a human decision.
var pathFlags = map[string]map[string]argHandlerKind{
	"builder build": {
		"--build-context": buildContextArg,
		"--cache-from":    builderCacheArg,
		"--cache-to":      builderCacheArg,
		"--file":          filePathArg,
		"-f":              filePathArg,
		"--iidfile":       outputPathArg,
		// --output and --secret are CSV; the builder-cache handler rewrites
		// their src= and dest= values.
		"--output": builderCacheArg,
		"-o":       builderCacheArg,
		"--secret": builderCacheArg,
	},
	"builder debug": {
		"--file":   filePathArg,
		"-f":       filePathArg,
		"--secret": builderCacheArg,
	},
	"compose": {
		"--env-file":          filePathArg,
		"--file":              filePathArg,
		"--f":                 filePathArg, // long-form spelling of the -f alias
		"-f":                  filePathArg,
		"--project-directory": filePathArg,
	},
	"compose run": {
		"--volume": volumeArg,
		"-v":       volumeArg,
	},
	"container create": {
		"--cidfile":    outputPathArg,
		"--cosign-key": filePathArg,
		"--env-file":   filePathArg,
		"--label-file": filePathArg,
		"--mount":      mountArg,
		"--pidfile":    outputPathArg,
		"--volume":     volumeArg,
		"-v":           volumeArg,
	},
	"container exec": {
		"--env-file": filePathArg,
	},
	"container run": {
		"--cidfile":    outputPathArg,
		"--cosign-key": filePathArg,
		"--env-file":   filePathArg,
		"--label-file": filePathArg,
		"--mount":      mountArg,
		"--pidfile":    outputPathArg,
		"--volume":     volumeArg,
		"-v":           volumeArg,
	},
	"image convert": {
		"--estargz-record-in":     filePathArg,
		"--zstdchunked-record-in": filePathArg,
	},
	"image decrypt": {
		"--key": filePathArg,
	},
	"image encrypt": {
		"--key": filePathArg,
	},
	"image load": {
		"--input": filePathArg,
		"-i":      filePathArg,
	},
	"image pull": {
		"--cosign-key": filePathArg,
	},
	"image push": {
		"--cosign-key": filePathArg,
	},
	"image save": {
		"--output": outputPathArg,
		"-o":       outputPathArg,
	},
}

// notPathFlags acknowledges flags that the generator's path heuristic
// matches but whose values contain no host path: guest-side paths, paths
// inside the container, format strings, and names.
var notPathFlags = map[string][]string{
	"": {
		"--cni-netconfpath", // guest-side
		"--cni-path",        // guest-side
		"--data-root",       // guest-side
		"--hosts-dir",       // guest-side
	},
	"builder debug": {
		"--image", // image name
	},
	"compose": {
		"--ipfs-address", // multiaddr
		"--profile",      // profile name
	},
	"compose exec": {
		"--workdir", // path inside the container
		"-w",
	},
	"compose run": {
		"--workdir", // path inside the container
		"-w",
	},
	"compose up": {
		"--scale", // SERVICE=NUM
	},
	"container attach": {
		"--detach-keys",
	},
	"container commit": {
		"--change", // Dockerfile instruction
		"-c",
	},
	"container create": {
		"--detach-keys",
		"--device",       // guest device path
		"--ipfs-address", // multiaddr
		"--log-driver",
		"--net",     // network name
		"--network", // network name
		"--tmpfs",   // path inside the container
		"--volumes-from",
		"--workdir", // path inside the container
		"-w",
	},
	"container exec": {
		"--workdir", // path inside the container
		"-w",
	},
	"container run": {
		"--detach-keys",
		"--device",       // guest device path
		"--ipfs-address", // multiaddr
		"--log-driver",
		"--net",     // network name
		"--network", // network name
		"--tmpfs",   // path inside the container
		"--volumes-from",
		"--workdir", // path inside the container
		"-w",
	},
	"container start": {
		"--detach-keys",
	},
	"image convert": {
		"--nydus-builder-path", // guest-side
		"--nydus-prefetch-patterns",
		"--nydus-work-dir", // guest-side
		"--overlaybd-fs-type",
	},
	"image decrypt": {
		"--gpg-homedir", // guest-side
	},
	"image encrypt": {
		"--gpg-homedir", // guest-side
		// --recipient takes prefix:value where value may be a key file
		// (jwe:/path/to/key.pem); such paths stay untranslated, so use a
		// guest path (/mnt/c/...) explicitly if needed.
		"--recipient",
	},
	"image pull": {
		"--ipfs-address", // multiaddr
	},
	"image push": {
		"--ipfs-address",      // multiaddr
		"--notation-key-name", // key name, not a path
	},
	"ipfs registry serve": {
		"--ipfs-address", // multiaddr
	},
}
