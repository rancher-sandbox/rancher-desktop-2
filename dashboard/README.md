# Cluster Explorer dashboard

This directory holds `pkg/rancher-deskboard`, our plugin for Rancher's
dashboard, and the Makefile that builds the dashboard with it. rdd embeds the
result and serves it through `rdd steve`.

The Makefile downloads a pinned commit of rancher-sandbox/dashboard, which is a
Rancher release tag plus our fixes to Rancher's shell. It copies the plugin
into that tree's `pkg/` folder and builds there, installing from Rancher's own
`package.json` and `yarn.lock`. So the dashboard gets the dependency versions
that Rancher builds its own release with, and this repository doesn't keep a
lockfile for it. Rancher's lockfile is for Yarn 1, so the build runs Yarn 1
through Corepack.

We download a checksummed archive instead of adding rancher-sandbox/dashboard
as a git submodule. With a submodule, every clone and worktree would have to
initialize it, and GitHub's source archives of this repository would leave the
dashboard source out.

The comments in the Makefile say how to move to a new Rancher tag or shell
commit.
