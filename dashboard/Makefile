# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Builds the Cluster Explorer dashboard and stages it for rdd to embed.
# README.md says why the build runs inside Rancher's dashboard tree.

# Rancher pairs this tag's shell with the steve version in rancher/rancher's
# go.mod at the same tag, so rdd/go.mod pins that version. A new tag also
# needs a new SHELL_COMMIT with our shell fixes on top of it.
RANCHER_TAG := v2.15.3
# This commit on rancher-sandbox/dashboard's i18n-fixes-2.15 branch is
# RANCHER_TAG plus our shell fixes. We pin the commit because a branch archive
# does not have a stable checksum. A new pin also needs RANCHER_SHA256 set to
# the new archive's checksum, or `make` stops at the check. Also tag the new
# commit rd2-shell-<its first 10 hex digits> on rancher-sandbox/dashboard, so
# GitHub keeps serving its archive after the branch is rewritten.
#
# A new shell can add keys to its en-us.yaml, change their English or remove
# them. In each YAML file under pkg/rancher-deskboard/translations, translate
# the new and changed keys and delete the removed ones. To list them, run
# `git diff <old> <new> -- shell/assets/translations/en-us.yaml` with the old
# and new SHELL_COMMIT in a rancher-sandbox/dashboard checkout. But keep the
# typeLabel and nav.group keys that en-us lacks, even the ones that en-us just
# removed. They name types and groups that the shell would otherwise show in
# English. If en-us's typeLabel messages change their arguments, change the
# extra ones too.
SHELL_COMMIT := d79889ccfab793c708c56ea973b38864396bdbcf
RANCHER_SHA256 := 625f4e637d18ca912690774a722b8820e5638dec0125283f2208d3230db52044

# Rancher's yarn.lock is a Yarn 1 lockfile. Naming the version keeps Corepack
# from picking the Yarn 4 that the repo root's package.json names.
YARN := corepack yarn@1.22.22

ARCHIVE := rancher-dashboard.tar.gz
TREE := rancher-dashboard
OUTPUT := dist/embed
STAGED := ../rdd/pkg/steve/dashboard

# The shell builds in every plugin under the tree's pkg/ folder, so the recipe
# keeps only rancher-components, which the shell imports, and adds ours.
# --ignore-scripts keeps install scripts off, as the repo root's .yarnrc.yml
# does for Yarn 4. Yarn 1 enforces engines fields. @achrinza/node-ipc, a Vue
# CLI dependency, lists Node only up to 25, so --ignore-engines lets it install
# on Node 26. Rancher's package.json doesn't name any browsers, so Browserslist
# would fall back to the repo root's list. That list targets Node 26, which the
# Browserslist version in Rancher's lock does not know. BROWSERSLIST=defaults
# gives the build the targets that Rancher's own build gets.
all:
	curl --fail --location --silent --show-error --output $(ARCHIVE) \
		https://github.com/rancher-sandbox/dashboard/archive/$(SHELL_COMMIT).tar.gz
	echo "$(RANCHER_SHA256)  $(ARCHIVE)" | shasum --algorithm 256 --check
	rm -rf $(TREE)
	mkdir -p $(TREE)
	tar --extract --gzip --file=$(ARCHIVE) --directory=$(TREE) --strip-components=1
	find $(TREE)/pkg -mindepth 1 -maxdepth 1 ! -name rancher-components -exec rm -rf {} +
	cp -R pkg/rancher-deskboard $(TREE)/pkg/
	cd $(TREE) && $(YARN) install --frozen-lockfile --ignore-scripts --ignore-engines
	cd $(TREE) && BROWSERSLIST=defaults RANCHER_ENV=desktop RESOURCE_BASE=http://127.0.0.1:6120 \
		OUTPUT_DIR=$(OUTPUT) COMMIT=$(RANCHER_TAG) DASHBOARD_VERSION=$(RANCHER_TAG) $(YARN) build
	find $(TREE)/$(OUTPUT) -name '*.map' -delete
	rm -rf $(STAGED)
	cp -R $(TREE)/$(OUTPUT) $(STAGED)
.PHONY: all
