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

ifneq ($(shell sh -c 'command -v winver.exe'),)
# On Windows, make sure we pass the environment variables back to RDD
export WSLENV := $(WSLENV):RDD_INSTANCE:RDD_DEVELOPER_MODE
# Use the Windows temporary directory
export TMPDIR := $(shell wslpath -u $$(cmd.exe /c "echo %TEMP%"))
endif

# Run BATS tests for BATS helpers
bats-helpers: check-bats
	$(BATS_CORE) helpers/
.PHONY: bats-helpers

# Run CLI tests
bats-cli: check-bats build-rdd
	RDD_INSTANCE=bats-cli $(BATS_CORE) tests/10-cli/
.PHONY: bats-cli

# Run service tests
bats-service: check-bats build-rdd
	RDD_INSTANCE=bats-service $(BATS_CORE) tests/20-service/
.PHONY: bats-service

# Run builtin controller tests
bats-builtin: check-bats build-rdd
	RDD_INSTANCE=bats-builtin-controller $(BATS_CORE) tests/30-builtin-controllers/
.PHONY: bats-builtin

# Run rdd API group controller tests
bats-rdd: check-bats build-rdd build-rdd-controller
	RDD_INSTANCE=bats-rdd-controller $(BATS_CORE) tests/31-rdd-controllers/
.PHONY: bats-rdd

# Run app API group controller tests
bats-app: check-bats build-rdd
	RDD_INSTANCE=bats-app-controller $(BATS_CORE) tests/32-app-controllers/
.PHONY: bats-app

# Run lima API group controller tests
bats-lima: check-bats build-rdd
	RDD_INSTANCE=bats-lima-controller $(BATS_CORE) tests/33-lima-controllers/
.PHONY: bats-lima

# Run all controller tests
bats-controllers: bats-builtin bats-rdd bats-app bats-lima
.PHONY: bats-controllers

# Run all BATS tests
bats-all: bats-helpers bats-cli bats-service bats-controllers
.PHONY: bats-all

# Run BATS tests with timeout to prevent hanging
bats-timeout: check-bats build-rdd
	timeout 600 $(BATS_CORE) tests/
.PHONY: bats-timeout

# Run specific BATS test file
# Usage: make bats-file FILE=bats/tests/31-rdd-controllers/configmapreplicaset.bats
bats-file: check-bats build-rdd build-all-controllers
	@if [ -z "$(FILE)" ]; then \
		echo "Usage: make bats-file FILE=path/to/test.bats"; \
		exit 1; \
	fi
	$(BATS_CORE) "$(FILE)"
.PHONY: bats-file

# Run BATS tests with trace output for debugging
# Usage: make bats-trace FILE=bats/tests/31-rdd-controllers/configmapreplicaset.bats
bats-trace: check-bats build-rdd build-all-controllers
	@if [ -z "$(FILE)" ]; then \
		echo "Usage: make bats-trace FILE=path/to/test.bats"; \
		exit 1; \
	fi
	RDD_TRACE=true $(BATS_CORE) "$(FILE)"
.PHONY: bats-trace

# List all RDD_INSTANCE names used by test targets (for CI log collection)
bats-instances:
	@grep -E '^[[:space:]]*RDD_INSTANCE=' $(MAKEFILE_LIST) | \
		sed 's/.*RDD_INSTANCE=//' | \
		cut -d' ' -f1 | \
		sort -u
.PHONY: bats-instances

# https://www.shellcheck.net/wiki/SC1091 -- Not following: xxx was not specified as input (see shellcheck -x)
# https://www.shellcheck.net/wiki/SC2034 -- xxx appears unused. Verify use (or export if used externally)
# https://www.shellcheck.net/wiki/SC2154 -- xxx is referenced but not assigned
# https://www.shellcheck.net/wiki/SC2312 -- Consider invoking this command separately to avoid masking its return value
TEST_FILES := $(shell find helpers tests -name '*.bash' -o -name '*.bats')
SC_EXCLUDES ?= SC1091,SC2034,SC2154,SC2312
lint: $(TEST_FILES)
	shellcheck --shell=bats --enable=all --exclude=$(SC_EXCLUDES) $(TEST_FILES)
	go tool shfmt --language-dialect=bats --indent=4 --diff $(TEST_FILES)
lint-fix: $(TEST_FILES)
	shellcheck --shell=bats --enable=all --exclude=$(SC_EXCLUDES) $(TEST_FILES)
	go tool shfmt --language-dialect=bats --indent=4 --write $(TEST_FILES)
.PHONY: lint lint-fix
