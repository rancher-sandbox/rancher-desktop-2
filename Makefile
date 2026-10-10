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

test-wix-helper:
	( cd src/go/wix-helper && go$(EXE) test ./... )
.PHONY: test-wix-helper

test-i18n-report:
	( cd src/go/i18n-report && go$(EXE) test ./... )
.PHONY: test-i18n-report

check-translations:
	( cd src/go/i18n-report && go$(EXE) run . check --locale=all )
.PHONY: check-translations

lint: lint-rdd lint-bats lint-shell lint-startup-profile lint-wix-helper
.PHONY: lint

lint-go: lint-rdd lint-startup-profile lint-wix-helper
.PHONY: lint-go

lint-rdd:
	$(MAKE) -C rdd lint-rdd
.PHONY: lint-rdd

lint-bats:
	$(MAKE) -C rdd lint-bats
.PHONY: lint-bats

lint-startup-profile:
	( cd src/go/startup-profile && go$(EXE) tool golangci-lint run )
.PHONY: lint-startup-profile

lint-wix-helper:
	( cd src/go/wix-helper && go$(EXE) tool golangci-lint run )
.PHONY: lint-wix-helper

# TODO: Currently, many of the scripts fail the check.
# SHELL_FILES := $(shell git ls-files -- '**/*.sh' ':!rdd/bats/')
SHELL_FILES := scripts/generate-rdd-client.sh

lint-shell:
	shellcheck --enable=all $(SHELL_FILES)
	go -C rdd tool shfmt --indent=4 --diff $(foreach f,$(SHELL_FILES),../$(f))
.PHONY: lint-shell

lint-shell-fix:
	shellcheck --enable=all $(SHELL_FILES)
	go -C rdd tool shfmt --indent=4 --write $(foreach f,$(SHELL_FILES),../$(f))
.PHONY: lint-shell-fix

check: spelling
.PHONY: check
