import {
  appArtifactName, artifactArch, artifactPlatform, rddArtifactName,
} from '../releaseArtifacts';

describe(appArtifactName, () => {
  test.each([
    ['darwin', 'arm64', 'dmg', 'rancher-desktop-2.0.0-alpha.3.darwin.aarch64.dmg'],
    ['darwin', 'x64', 'zip', 'rancher-desktop-2.0.0-alpha.3.darwin.x86_64.zip'],
    ['win32', 'x64', 'msi', 'rancher-desktop-2.0.0-alpha.3.windows.x86_64.msi'],
    ['linux', 'arm64', 'zip', 'rancher-desktop-2.0.0-alpha.3.linux.aarch64.zip'],
  ] as const)('names the %s %s %s', (platform, arch, ext, expected) => {
    expect(appArtifactName('2.0.0-alpha.3', platform, arch, ext)).toEqual(expected);
  });
});

describe(rddArtifactName, () => {
  test.each([
    ['darwin', 'arm64', 'rdd.2.0.0-alpha.3.darwin.aarch64'],
    ['linux', 'x64', 'rdd.2.0.0-alpha.3.linux.x86_64'],
    ['win32', 'x64', 'rdd.2.0.0-alpha.3.windows.x86_64.exe'],
  ] as const)('names the %s %s binary', (platform, arch, expected) => {
    expect(rddArtifactName('2.0.0-alpha.3', platform, arch)).toEqual(expected);
  });

  test('rejects a version that does not start with a digit', () => {
    expect(() => rddArtifactName('v2.1.0', 'darwin', 'arm64')).toThrow(/v2\.1\.0/);
  });
});

test('rejects a platform without an artifact name', () => {
  expect(() => artifactPlatform('freebsd')).toThrow(/freebsd/);
});

test('rejects an architecture without an artifact name', () => {
  expect(() => artifactArch('ia32')).toThrow(/ia32/);
});
