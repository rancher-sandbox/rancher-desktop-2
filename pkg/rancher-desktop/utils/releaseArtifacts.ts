/**
 * Names of release artifacts: `rancher-desktop-<version>.<platform>.<arch>.<ext>`
 * for the application, `rdd.<version>.<platform>.<arch>[.exe]` for the daemon.
 * The build writes these names and the updater looks them up.
 */

export type ArtifactPlatform = 'darwin' | 'linux' | 'windows';
export type ArtifactArch = 'aarch64' | 'x86_64';

/** electron-builder's `${ext}` macro, for an `artifactName` shared by several targets. */
export const EXT_MACRO = `\${ext}`;

export function artifactPlatform(platform: string): ArtifactPlatform {
  switch (platform) {
  case 'darwin':
  case 'linux':
    return platform;
  case 'win32':
    return 'windows';
  }
  throw new Error(`No release artifact name for platform ${ platform }`);
}

/**
 * No artifact name may contain `arm64`, because electron-updater's MacUpdater
 * treats any update file whose URL contains it as an arm64 build.
 */
export function artifactArch(arch: string): ArtifactArch {
  switch (arch) {
  case 'arm64':
    return 'aarch64';
  case 'x64':
    return 'x86_64';
  }
  throw new Error(`No release artifact name for architecture ${ arch }`);
}

/** The part of an application artifact's name after its version. */
export function appArtifactSuffix(platform: string, arch: string, ext: string): string {
  return `.${ artifactPlatform(platform) }.${ artifactArch(arch) }.${ ext }`;
}

export function appArtifactName(version: string, platform: string, arch: string, ext: string): string {
  return `rancher-desktop-${ version }${ appArtifactSuffix(platform, arch, ext) }`;
}

export function rddArtifactName(version: string, platform: string, arch: string): string {
  const exe = platform === 'win32' ? '.exe' : '';

  return `rdd.${ version }.${ artifactPlatform(platform) }.${ artifactArch(arch) }${ exe }`;
}
