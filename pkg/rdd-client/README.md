# rdd-client

This is a mostly-generated client to use the [Rancher Desktop Daemon] API from
JavaScript.  We originally used a forked version of [`@kubernetes/client-node`],
but it had issues working in a browser environment and hacking it up caused
issues with using it as a local package.

Many of the files here are still straight copies from
[`@kubernetes/client-node`], with trivial linting fixes.

[Rancher Desktop Daemon]: https://github.com/rancher-sandbox/rancher-desktop-daemon
[`@kubernetes/client-node`]: https://www.npmjs.com/package/@kubernetes/client-node

## Building

We do not actually install this as a separate package, so the only build step is
for regenerating the API definitions.  To do so:

- Have a running dockerd; at this point, this means running a separate instance
  of Rancher Desktop, but in the future `rdd` is expected to be sufficient.
- Ensure that `rdd` is in your `PATH`.
- Run `yarn generate:rdd-client` from the [`rancher-desktop-app`] repository.

## Usage

We export a `KubeConfig` class; it is distinct from the one from
[`@kubernetes/client-node`], but should be API-compatible enough for our use.
The other files are copies from upstream (at 1.4.0) with small compilation fixes.

[`rancher-desktop-app`]: https://github.com/rancher-sandbox/rancher-desktop-app
