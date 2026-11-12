## Release Checklist

- [ ] Update version number in package.json if not done after last release.
- [ ] Tag release branch. Wait for the CI to build artifacts.
- [ ] Sign windows installer and standalone `rdd.exe` for both architectures.
- [ ] Sign mac zips and standalone `rdd` for both architectures.

### Release Documentation
- [ ] Release notes. Update on the GitHub draft Release page.
- [ ] docs update (Help, Readme..)
- [ ] Slack Announcements
- [ ] Newsletter summary
- [ ] Update metrics, roadmap on Confluence page

### Release
- [ ] Upload the release artifacts to the GitHub draft Release page under the names the build gave them. Take the signed ones from `yarn sign`'s `dist/`, because the Package run's copies under the same names are unsigned.
  - [ ] macOS aarch64: the dmg, zip, and `rdd` from `yarn sign`, and the `.sha512sum` of each.
  - [ ] macOS x86_64: the dmg, zip, and `rdd` from `yarn sign`, and the `.sha512sum` of each.
  - [ ] Windows x86_64: the msi and `rdd.exe` from `yarn sign`, and the `.sha512sum` of each.
  - [ ] Windows aarch64: the msi and `rdd.exe` from `yarn sign`, and the `.sha512sum` of each.
  - [ ] Linux x86_64: the zip and `rdd` from the Package run. Create a `.sha512sum` for each.
  - [ ] Linux aarch64: the zip and `rdd` from the Package run. Create a `.sha512sum` for each.
- [ ] Perform smoke test on release artifacts.
- [ ] Update the release version for upgrade responder.
- [ ] Move from draft release to Release.
- [ ] Check the auto update functionality.
