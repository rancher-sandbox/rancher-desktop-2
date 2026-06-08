#!/bin/bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

set -o errexit -o nounset

API_GROUPS=$(
	cd pkg/apis
	# shellcheck disable=SC2012
	ls -d -- */ | tr -d /
)

# Generate deepcopy for each API group
for apigroup in $API_GROUPS; do
	go tool controller-gen applyconfiguration object "paths=./pkg/apis/$apigroup/..."
done
