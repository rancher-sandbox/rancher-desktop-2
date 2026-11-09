/**
 * Names of release artifacts: `rancher-desktop-<version>.<platform>.<arch>.<ext>`
 * for the application, `rdd.<version>.<platform>.<arch>[.exe]` for the daemon.
 * The build writes these names and the updater looks them up. The app also
 * finds rdd in a resources directory named by artifactPlatform.
 *
 * Build scripts load this module in plain Node, so do not import anything here
 * that works only in Electron's main process, such as a native module built
 * for Electron.
 */

export type ArtifactPlatform = 'darwin' | 'linux' | 'windows';
export type ArtifactArch = 'aarch64' | 'x86_64';

/** electron-builder's `${ext}` macro, for an `artifactName` shared by several targets. */
export const EXT_MACRO = `\${ext}`;

export function artifactPlatform(platform: NodeJS.Platform): ArtifactPlatform {
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
export function artifactArch(arch: NodeJS.Architecture): ArtifactArch {
  switch (arch) {
  case 'arm64':
    return 'aarch64';
  case 'x64':
    return 'x86_64';
  }
  throw new Error(`No release artifact name for architecture ${ arch }`);
}

/** The part of an application artifact's name after its version. */
export function appArtifactSuffix(platform: NodeJS.Platform, arch: NodeJS.Architecture, ext: string): string {
  return `.${ artifactPlatform(platform) }.${ artifactArch(arch) }.${ ext }`;
}

export function appArtifactName(version: string, platform: NodeJS.Platform, arch: NodeJS.Architecture, ext: string): string {
  return `rancher-desktop-${ version }${ appArtifactSuffix(platform, arch, ext) }`;
}

/**
 * rdd runs under this name only when it starts with `rdd.` and a digit (see
 * isReleaseName in rdd/cmd/rdd/multicall.go), so rddArtifactName throws
 * unless the version starts with a digit.
 */
export function rddArtifactName(version: string, platform: NodeJS.Platform, arch: NodeJS.Architecture): string {
  if (!/^\d/.test(version)) {
    throw new Error(`No rdd artifact name for version ${ version }: rdd runs under its release file name only if the version starts with a digit`);
  }
  const exe = platform === 'win32' ? '.exe' : '';

  return `rdd.${ version }.${ artifactPlatform(platform) }.${ artifactArch(arch) }${ exe }`;
}
