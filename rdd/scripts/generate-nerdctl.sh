#!/bin/bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Regenerate the nerdctl parse table (pkg/nerdctlstub). The generate stage
# only builds and runs on Linux, so elsewhere it is cross-compiled and run
# in a Linux container (docker, or $CONTAINER_ENGINE).

set -o errexit -o nounset

cd "$(dirname "$0")/../pkg/nerdctlstub/generate"

go run ./extract

if [ "$(go env GOOS)" = "linux" ]; then
	go run .
else
	binary=generate-nerdctl.tmp
	trap 'rm -f "$binary"' EXIT
	GOOS=linux go build -o "$binary" .
	"${CONTAINER_ENGINE:-docker}" run --rm \
		--volume "$(git rev-parse --show-toplevel):/work" \
		--workdir /work/rdd/pkg/nerdctlstub/generate \
		alpine "./$binary"
fi
