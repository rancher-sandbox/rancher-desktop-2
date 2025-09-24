# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# BATS integration testing targets
BATS_CORE := ./lib/bats-core/bin/bats

default: bats-all

# Check if BATS core exists
check-bats:
	@if [ ! -f "$(BATS_CORE)" ]; then \
		echo "BATS core not found. Please run 'git submodule update --init --recursive' to initialize BATS submodules." >&2; \
		exit 1; \
	fi
.PHONY: check-bats

# Targets for the top level
TOPLEVEL_TARGETS := build-rdd build-rdd-controller build-all-controllers
$(TOPLEVEL_TARGETS):
	@$(MAKE) -C .. $@
.PHONY: $(TOPLEVEL_TARGETS)

# Run BATS tests for BATS helpers
bats-helpers: check-bats
	$(BATS_CORE) helpers/
.PHONY: bats-helpers

# Run CLI tests
bats-cli: check-bats build-rdd
	PATH="$(PWD)/../bin:$$PATH" RDD_INSTANCE=bats-cli $(BATS_CORE) tests/10-cli/
.PHONY: bats-cli

# Run service tests
bats-service: check-bats build-rdd
	PATH="$(PWD)/../bin:$$PATH" RDD_INSTANCE=bats-service $(BATS_CORE) tests/20-service/
.PHONY: bats-service

# Run RDD API group controller tests
bats-rdd: check-bats build-rdd build-rdd-controller
	PATH="$(PWD)/../bin:$$PATH" RDD_INSTANCE=bats-rdd-controller $(BATS_CORE) tests/31-rdd-controllers/
.PHONY: bats-rdd

# Run app API group controller tests
bats-app: check-bats build-rdd
	PATH="$(PWD)/../bin:$$PATH" RDD_INSTANCE=bats-app-controller $(BATS_CORE) tests/32-app-controllers/
.PHONY: bats-app

# Run all controller tests
bats-controllers: bats-rdd bats-app
.PHONY: bats-controllers

# Run all BATS tests
bats-all: bats-helpers bats-cli bats-service bats-controllers
.PHONY: bats-all

# Run BATS tests with timeout to prevent hanging
bats-timeout: check-bats build-rdd
	PATH="$(PWD)/../bin:$$PATH" timeout 600 $(BATS_CORE) tests/
.PHONY: bats-timeout

# Run specific BATS test file
# Usage: make bats-file FILE=bats/tests/31-rdd-controllers/configmapreplicaset.bats
bats-file: check-bats build-rdd build-all-controllers
	@if [ -z "$(FILE)" ]; then \
		echo "Usage: make bats-file FILE=path/to/test.bats"; \
		exit 1; \
	fi
	PATH="$(PWD)/../bin:$$PATH" $(BATS_CORE) "$(FILE)"
.PHONY: bats-file

# Run BATS tests with trace output for debugging
# Usage: make bats-trace FILE=bats/tests/31-rdd-controllers/configmapreplicaset.bats
bats-trace: check-bats build-rdd build-all-controllers
	@if [ -z "$(FILE)" ]; then \
		echo "Usage: make bats-trace FILE=path/to/test.bats"; \
		exit 1; \
	fi
	PATH="$(PWD)/../bin:$$PATH" RDD_TRACE=true $(BATS_CORE) "$(FILE)"
.PHONY: bats-trace

# https://www.shellcheck.net/wiki/SC1091 -- Not following: xxx was not specified as input (see shellcheck -x)
# https://www.shellcheck.net/wiki/SC2034 -- xxx appears unused. Verify use (or export if used externally)
# https://www.shellcheck.net/wiki/SC2154 -- xxx is referenced but not assigned
TEST_FILES := $(shell find helpers tests -name '*.bash' -o -name '*.bats')
SC_EXCLUDES ?= SC1091,SC2034,SC2154
lint: $(TEST_FILES)
	shellcheck --shell=bats --exclude=$(SC_EXCLUDES) $(TEST_FILES)
	shfmt --language-dialect=bats --indent=4 --diff $(TEST_FILES)
lint-fix: $(TEST_FILES)
	shellcheck --shell=bats --exclude=$(SC_EXCLUDES) $(TEST_FILES)
	shfmt --language-dialect=bats --indent=4 --write $(TEST_FILES)
.PHONY: lint lint-fix
