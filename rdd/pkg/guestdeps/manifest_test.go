// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package guestdeps

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// sampleManifest is a manifest in the shape `yarn rddepman guest` writes: a
// dependency shipping one asset per architecture and image variant, and one
// shipping a single arch-independent asset.
const sampleManifest = `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      arch: amd64
      variant: raw
      url: https://example.test/distro.v0.2.7.amd64.raw.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
    - platform: linux
      arch: amd64
      variant: tar
      url: https://example.test/distro.v0.2.7.amd64.tar.xz
      checksum: sha256:a6d9e52dbafa69138ac84d2812b01c158ba3b19cf7c8725edefcee0b776afb61
    - platform: linux
      arch: arm64
      variant: raw
      url: https://example.test/distro.v0.2.7.arm64.raw.xz
      checksum: sha256:c03fb8f0ee367d608498adddaf3633491f803f4adab2d4a6499c2d9fb271ec76
config:
  version: 1.0.0
  assets:
    - platform: linux
      url: https://example.test/config-1.0.0.tar.gz
      checksum: sha256:4389cf04704d1b37317ab3e8dc7bec7611f1503af6efc3f9a68f872bcddf806a
`

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dependencies.yaml")
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func loadSample(t *testing.T) Manifest {
	t.Helper()
	m, err := LoadManifest(writeManifest(t, sampleManifest))
	assert.NilError(t, err)
	return m
}

func TestLoadManifest(t *testing.T) {
	m := loadSample(t)

	assert.Equal(t, m["distro"].Version, "0.2.7")
	assert.Equal(t, len(m["distro"].Assets), 3)
	assert.Equal(t, m["distro"].Assets[0].URL, "https://example.test/distro.v0.2.7.amd64.raw.xz")
	assert.Equal(t, m["config"].Assets[0].Arch, "", "an arch-independent asset records no arch")
}

func TestLoadManifestRejectsUnusableEntries(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"no version": {want: "has no version", content: `
distro:
  assets:
    - platform: linux
      url: https://example.test/distro.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		"unprefixed checksum": {want: "want sha256:", content: `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: https://example.test/distro.xz
      checksum: ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		"misspelled checksum key": {want: `checksum ""`, content: `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: https://example.test/distro.xz
      checkum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		"plain http url": {want: `url "http://example.test/distro.xz", want https`, content: `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: http://example.test/distro.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		"url with no file name": {want: "no file name", content: `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: https://example.test/
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		// url.Parse accepts a url with no host, which reaches the download and
		// fails there, a full retry schedule into the build.
		"url with no host": {want: "with no host", content: `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: https:///distro.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		// path.Base would hand the directory's own name to the download.
		"url naming a directory": {want: "no file name", content: `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: https://example.test/releases/
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		"url ending in a traversal": {want: "no file name", content: `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: https://example.test/releases/..
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		// url.Parse decodes %5C into a backslash, which filepath.Join treats as
		// a separator on Windows, so path.Base alone would hand cachePath a
		// traversal there.
		"url whose file name escapes on windows": {want: "no file name", content: `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: https://example.test/..%5C..%5C..%5Cescape.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
		// cachePath joins the version into the download's directory, so a
		// version escaping it would relocate the whole entry.
		"version that escapes the cache directory": {want: "want a single directory name", content: `
distro:
  version: ../../../escape
  assets:
    - platform: linux
      url: https://example.test/distro.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadManifest(writeManifest(t, tc.content))
			assert.ErrorContains(t, err, "dependency distro in ")
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// cachePath joins the dependency name into the download's directory alongside
// the version and the file name, so a name escaping it would relocate the entry.
func TestLoadManifestRejectsAnEscapingDependencyName(t *testing.T) {
	_, err := LoadManifest(writeManifest(t, `
"../../../escape":
  version: 0.2.7
  assets:
    - platform: linux
      url: https://example.test/distro.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`))
	assert.ErrorContains(t, err, `dependency "../../../escape" in `)
	assert.ErrorContains(t, err, "not a single directory name")
}

// Dots are ordinary in a release file name, so only the traversal itself is
// rejected.
func TestLoadManifestAcceptsDotsInAFileName(t *testing.T) {
	m, err := LoadManifest(writeManifest(t, `
distro:
  version: 0.2.7
  assets:
    - platform: linux
      url: https://example.test/distro..amd64.raw.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`))
	assert.NilError(t, err)
	assert.Equal(t, m["distro"].Assets[0].URL, "https://example.test/distro..amd64.raw.xz")
}

func TestSelect(t *testing.T) {
	m := loadSample(t)

	dep, err := m.Select("distro", Selector{Platform: "linux", Arch: "amd64", Variant: "tar"})
	assert.NilError(t, err)
	assert.Equal(t, dep.Name, "distro")
	assert.Equal(t, dep.Version, "0.2.7")
	assert.Equal(t, dep.Asset.URL, "https://example.test/distro.v0.2.7.amd64.tar.xz")
}

func TestSelectArchIndependentAsset(t *testing.T) {
	m := loadSample(t)

	dep, err := m.Select("config", Selector{Platform: "linux", Arch: "arm64"})
	assert.NilError(t, err)
	assert.Equal(t, dep.Asset.URL, "https://example.test/config-1.0.0.tar.gz")
}

func TestSelectRejectsAmbiguousAndMissingAssets(t *testing.T) {
	m := loadSample(t)

	// Both amd64 variants match a selector that names no variant.
	_, err := m.Select("distro", Selector{Platform: "linux", Arch: "amd64"})
	assert.ErrorContains(t, err, "found 2")

	_, err = m.Select("distro", Selector{Platform: "linux", Arch: "arm64", Variant: "tar"})
	assert.ErrorContains(t, err, "found 0")

	_, err = m.Select("nerdctl", Selector{Platform: "linux", Arch: "amd64"})
	assert.ErrorContains(t, err, `no dependency "nerdctl"`)
}
