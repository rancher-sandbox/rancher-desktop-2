# Packages

RDD packages are the universal concept to bundle and transport a set of resources.  Conceptually a package is a directory tree with file resources, and potentially a manifest file. They are the spiritual successor of the `resources` directory in Rancher Desktop 1.x.

The package can be stored in different formats:

1. Embedded in the `rdd` binary itself
2. A directory on disk
3. A (compressed) tarball

## Usage scenarios

### Bundled resources

### Optional packages

#### Wasm bundle

#### AI bundle

### Snapshots

Snapshots come in different variants: VM snapshots, namespace snapshots, and RDD instance snapshots.

All of them can be store in an RDD package. One package may include multiple snapshots.

### Airgap installation bundles <!-- spellchecker:ignore -->

It is possible to bundle all

## Other

### Manifest

### Multi-platform

Packages can be multi-platform: they may bundle utilities for multiple OSes.

### Signatures
