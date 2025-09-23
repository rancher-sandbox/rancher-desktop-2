# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors
# SPDX-FileCopyrightText: The KCP Authors

KUBE_VERSION := $(shell go list -m -f '{{.Version}}' 'k8s.io/kubernetes')
KUBE_MAJOR_VERSION := $(shell echo $(KUBE_VERSION) | sed 's/v\([0-9]*\).*/\1/')
KUBE_MINOR_VERSION := $(shell echo $(KUBE_VERSION) | sed "s/v[0-9]*\.\([0-9]*\).*/\1/")
GIT_COMMIT := $(shell git rev-parse --short HEAD || echo 'local')
GIT_DIRTY := $(shell git diff --quiet && echo 'clean' || echo 'dirty')
GIT_VERSION := $(KUBE_VERSION)+rdd-$(shell git describe --tags --match='v*' --abbrev=14 "$(GIT_COMMIT)^{commit}" 2>/dev/null || echo v0.0.0-$(GIT_COMMIT))
RDD_VERSION := $(shell git describe --match 'v[0-9]*' --dirty='.m' --always --tags)
RDD_VERSION_TRIMMED := $(RDD_VERSION:v%=%)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
PACKAGE := github.com/rancher-sandbox/rancher-desktop-daemon
LDFLAGS := \
	-X k8s.io/client-go/pkg/version.gitCommit=${GIT_COMMIT} \
	-X k8s.io/client-go/pkg/version.gitTreeState=${GIT_DIRTY} \
	-X k8s.io/client-go/pkg/version.gitVersion=${GIT_VERSION} \
	-X k8s.io/client-go/pkg/version.gitMajor=${KUBE_MAJOR_VERSION} \
	-X k8s.io/client-go/pkg/version.gitMinor=${KUBE_MINOR_VERSION} \
	-X k8s.io/client-go/pkg/version.buildDate=${BUILD_DATE} \
	\
	-X k8s.io/component-base/version.gitCommit=${GIT_COMMIT} \
	-X k8s.io/component-base/version.gitTreeState=${GIT_DIRTY} \
	-X k8s.io/component-base/version.gitVersion=${GIT_VERSION} \
	-X k8s.io/component-base/version.gitMajor=${KUBE_MAJOR_VERSION} \
	-X k8s.io/component-base/version.gitMinor=${KUBE_MINOR_VERSION} \
	-X k8s.io/component-base/version.buildDate=${BUILD_DATE} \
	\
	-X $(PACKAGE)/pkg/version.Version=$(RDD_VERSION) \
	-X $(PACKAGE)/pkg/version.GitCommit=${GIT_COMMIT} \
	-X $(PACKAGE)/pkg/version.BuildDate=${BUILD_DATE} \
	-s -w

TAGS := sqlite_omit_load_extension

default: build-rdd
.PHONY: default

ldflags:
	@echo $(LDFLAGS)

build: build-rdd build-all-controllers
.PHONY: build

build-rdd:
	CGO_CFLAGS="-DSQLITE_ENABLE_DBSTAT_VTAB=1 -DSQLITE_USE_ALLOCA=1" \
	CGO_ENABLED=1 go build -tags="$(TAGS)" -buildvcs=false -gcflags="all=${GCFLAGS}" -ldflags="$(LDFLAGS)" -o bin/rdd ./cmd/rdd
	ls -lh ./bin/rdd
.PHONY: build-rdd

# API Group Controller management - Auto-discovery of API groups
API_GROUPS := $(notdir $(shell find pkg/controllers -type d -mindepth 1 -maxdepth 1 -not -name base))

# Generate build targets for API group controllers
define API_GROUP_CONTROLLER_TARGETS
build-$(1)-controller:
	CGO_ENABLED=0 go build -tags="$(TAGS)" -buildvcs=false -gcflags="all=$${GCFLAGS}" -ldflags="$(LDFLAGS)" -o bin/$(1)-controller ./cmd/$(1)-controller
	ls -lh ./bin/$(1)-controller
.PHONY: build-$(1)-controller

test-$(1)-controllers:
	go test -v ./pkg/controllers/$(1)/...
.PHONY: test-$(1)-controllers

run-$(1)-controller:
	./bin/$(1)-controller
.PHONY: run-$(1)-controller
endef

# Generate targets for each API group
$(foreach apigroup,$(API_GROUPS),$(eval $(call API_GROUP_CONTROLLER_TARGETS,$(apigroup))))

# Meta targets
build-all-controllers: $(addprefix build-,$(addsuffix -controller,$(API_GROUPS)))
.PHONY: build-all-controllers

test-all-controllers: $(addprefix test-,$(addsuffix -controllers,$(API_GROUPS)))
.PHONY: test-all-controllers

run-all-controllers: $(addprefix run-,$(addsuffix -controller,$(API_GROUPS)))
.PHONY: run-all-controllers

run:
	./bin/rdd start
.PHONY: run

lint:
	golangci-lint run
.PHONY: lint

format:
	golangci-lint fmt
.PHONY: format

ltag:
	# exclude bats/lib, but --excludes only takes a dir name, not a path name
	go tool ltag -v -t .ltag -path . --excludes=lib
.PHONY: ltag

# BATS integration testing targets
BATS_CORE := ./bats/lib/bats-core/bin/bats

# Check if BATS core exists
check-bats:
	@if [ ! -f "$(BATS_CORE)" ]; then \
		echo "BATS core not found. Please run 'git submodule update --init --recursive' to initialize BATS submodules."; \
		exit 1; \
	fi
.PHONY: check-bats

# Run BATS tests for BATS helpers
bats-helpers: check-bats
	$(BATS_CORE) bats/helpers/
.PHONY: bats-helpers

# Run CLI tests
bats-cli: check-bats build-rdd
	PATH="$(PWD)/bin:$$PATH" RDD_INSTANCE=bats-cli $(BATS_CORE) bats/tests/10-cli/
.PHONY: bats-cli

# Run service tests
bats-service: check-bats build-rdd
	PATH="$(PWD)/bin:$$PATH" RDD_INSTANCE=bats-service $(BATS_CORE) bats/tests/20-service/
.PHONY: bats-service

# Run RDD API group controller tests
bats-rdd: check-bats build-rdd build-rdd-controller
	PATH="$(PWD)/bin:$$PATH" RDD_INSTANCE=bats-rdd-controller $(BATS_CORE) bats/tests/31-rdd-controllers/
.PHONY: bats-rdd

# Run app API group controller tests
bats-app: check-bats build-rdd
	PATH="$(PWD)/bin:$$PATH" RDD_INSTANCE=bats-app-controller $(BATS_CORE) bats/tests/32-app-controllers/
.PHONY: bats-app

# Run all controller tests
bats-controllers: bats-rdd bats-app
.PHONY: bats-controllers

# Run all BATS tests
bats-all: bats-cli bats-service bats-controllers
.PHONY: bats-all

# Run BATS tests with timeout to prevent hanging
bats-timeout: check-bats build-rdd
	PATH="$(PWD)/bin:$$PATH" timeout 600 $(BATS_CORE) bats/tests/
.PHONY: bats-timeout

# Run specific BATS test file
# Usage: make bats-file FILE=bats/tests/31-rdd-controllers/configmapreplicaset.bats
bats-file: check-bats build-rdd build-all-controllers
	@if [ -z "$(FILE)" ]; then \
		echo "Usage: make bats-file FILE=path/to/test.bats"; \
		exit 1; \
	fi
	PATH="$(PWD)/bin:$$PATH" $(BATS_CORE) "$(FILE)"
.PHONY: bats-file

# Run BATS tests with trace output for debugging
# Usage: make bats-trace FILE=bats/tests/31-rdd-controllers/configmapreplicaset.bats
bats-trace: check-bats build-rdd build-all-controllers
	@if [ -z "$(FILE)" ]; then \
		echo "Usage: make bats-trace FILE=path/to/test.bats"; \
		exit 1; \
	fi
	PATH="$(PWD)/bin:$$PATH" RDD_TRACE=true $(BATS_CORE) "$(FILE)"
.PHONY: bats-trace

clean:
	-rm -r bin
.PHONY: clean
