#!/bin/bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

set -o errexit -o nounset

# Generate deepcopy and apply configurations for each API version package.
# The paths leave out the generated applyconfiguration packages, because
# controller-gen lists their imports before it rewrites them and then fails to
# type-check a file whose imports changed.
for apiversion in pkg/apis/*/*/; do
    go tool controller-gen applyconfiguration object "paths=./${apiversion%/}"
done
