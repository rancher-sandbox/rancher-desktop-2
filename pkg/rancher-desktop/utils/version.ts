import semver from 'semver';

/**
 * Whether the version string is a plain semver release with no pre-release
 * identifiers.  Development builds (git-describe output like `2.0.0-9-gabc1234`),
 * alpha/beta pre-releases, and unparsable versions all count as non-release, so
 * the pre-release styling (striped icon and nav) covers everything short of a
 * final release.
 */
export function isReleaseVersion(version: string): boolean {
  return semver.valid(version) !== null && semver.prerelease(version) === null;
}
