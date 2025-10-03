# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors
# SPDX-FileCopyrightText: The KCP Authors

EXE := $(if $(shell sh -c 'command -v winver.exe'),.exe,)
KUBE_VERSION := $(shell go$(EXE) list -m -f '{{.Version}}' 'k8s.io/kubernetes')
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

GOLANG_SOURCES := $(shell find . -name '*.go') go.mod go.sum

bin/rdd$(EXE): $(GOLANG_SOURCES)
	CGO_CFLAGS="-DSQLITE_ENABLE_DBSTAT_VTAB=1 -DSQLITE_USE_ALLOCA=1" \
	CGO_ENABLED=1 go build -tags="$(TAGS)" -buildvcs=false -gcflags="all=${GCFLAGS}" -ldflags="$(LDFLAGS)" -o $@ ./cmd/rdd
	ls -lh $@
build-rdd: bin/rdd$(EXE)
.PHONY: build-rdd

# API Group Controller management - Auto-discovery of API groups
API_GROUPS := $(notdir $(shell find pkg/controllers -type d -mindepth 1 -maxdepth 1 -not -name base))

# Generate build targets for API group controllers
define API_GROUP_CONTROLLER_TARGETS
bin/$(1)-controller$$(EXE): $$(GOLANG_SOURCES)
	CGO_ENABLED=0 go build -tags="$(TAGS)" -buildvcs=false -gcflags="all=$${GCFLAGS}" -ldflags="$(LDFLAGS)" -o $$@ ./cmd/$(1)-controller
	ls -lh $$@
build-$(1)-controller: bin/$(1)-controller$(EXE)
.PHONY: build-$(1)-controller

test-$(1)-controllers:
	go test -v ./pkg/controllers/$(1)/...
.PHONY: test-$(1)-controllers

run-$(1)-controller: bin/$(1)-controller$(EXE)
	$<
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

run: bin/rdd$(EXE)
	$< start
.PHONY: run

lint:
	$(MAKE) -C bats lint
	golangci-lint run
.PHONY: lint

format:
	golangci-lint fmt
.PHONY: format

.github/actions/spelling/expect/golang-generated.txt: scripts/spell-check-generate-golang-expect.go $(GOLANG_SOURCES)
	go$(EXE) run $<
spelling: scripts/check-spelling.sh .github/actions/spelling/expect/golang-generated.txt
	$<
.PHONY: spelling

ltag:
	# exclude bats/lib, but --excludes only takes a dir name, not a path name
	go tool ltag -v -t .ltag -path . --excludes='lib check-spelling'
check-ltag:
	go tool ltag -v -t .ltag -path . --excludes='lib check-spelling' --check
.PHONY: ltag check-ltag

BATS_TARGETS := $(shell $(MAKE) -C bats --print-data-base --question --no-builtin-variables | awk -F: '$$1 ~ /^bats-/ { print $$1 }')
$(BATS_TARGETS):
	@$(MAKE) -C bats $@
.PHONY: $(BATS_TARGETS)

clean:
	-rm -r bin
.PHONY: clean
