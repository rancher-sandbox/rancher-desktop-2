# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Repository-wide checks. The daemon's build, test, and license-header (ltag)
# targets live in rdd/Makefile. Spelling covers the whole tree; ltag is scoped to
# rdd/ and will widen to the whole repository later.

EXE := $(if $(shell sh -c 'command -v winver.exe'),.exe,)

GOLANG_SOURCES := $(shell find . -name '*.go')

default: check
.PHONY: default

.github/actions/spelling/expect/golang-generated.txt: scripts/spell-check-generate-golang-expect.go $(GOLANG_SOURCES)
	go$(EXE) run $<
spelling: scripts/check-spelling.sh .github/actions/spelling/expect/golang-generated.txt
	$<
.PHONY: spelling

check: spelling
.PHONY: check
