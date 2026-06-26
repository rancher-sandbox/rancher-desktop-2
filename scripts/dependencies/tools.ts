import path from 'path';

import { defined } from '@/pkg/rancher-desktop/utils/typeUtils';
import {
  AssetPlatform,
  assetChecksum,
  DependencyAsset,
  DownloadContext,
  downloadAndHash,
  fetchUpstreamChecksums,
  GitHubDependency,
  GlobalDependency,
  GoArch,
  Platform,
  selectAsset,
  selectAssets,
} from '@/scripts/lib/dependencies';
import {
  download,
  downloadTarGZ,
} from '@/scripts/lib/download';

function exeName(contextOrPlatform: DownloadContext | Platform | 'windows', name: string) {
  const platform = typeof contextOrPlatform === 'string' ? contextOrPlatform : contextOrPlatform.platform;

  return `${ name }${ platform.startsWith('win') ? '.exe' : '' }`;
}

/** The suffix upstream appends to Windows artifact filenames. */
function exeSuffix(platform: AssetPlatform): string {
  return platform === 'windows' ? '.exe' : '';
}

export function cartesian<A, B>(
  as: readonly A[],
  bs: readonly B[],
): [A, B][] {
  return as.flatMap(a => bs.map<[A, B]>(b => [a, b]));
}

/** The host platforms most dependencies publish for. */
const HOST_PLATFORMS: readonly AssetPlatform[] = ['linux', 'darwin', 'windows'];
const ARCHES: readonly GoArch[] = ['amd64', 'arm64'];

export class Helm extends GlobalDependency(GitHubDependency) {
  readonly name = 'helm';
  readonly githubOwner = 'helm';
  readonly githubRepo = 'helm';

  async download(context: DownloadContext): Promise<void> {
    // Download Helm. It is a tar.gz file that needs to be expanded and file moved.
    const asset = selectAsset(context, this.name, { platform: context.goPlatform, arch: context.goArch });

    await downloadTarGZ(asset.url, path.join(context.binDir, exeName(context, 'helm')), {
      expectedChecksum: assetChecksum(asset),
      entryName:        `${ context.goPlatform }-${ context.goArch }/${ exeName(context, 'helm') }`,
    });
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    return Promise.all(cartesian(HOST_PLATFORMS, ARCHES).map(async([platform, arch]) => {
      const archiveName = `helm-v${ version }-${ platform }-${ arch }.tar.gz`;
      const url = `https://get.helm.sh/${ archiveName }`;
      // Helm publishes a sidecar `.sha256sum` per artifact, one line of `<hex>  <filename>`.
      const sidecar = await fetchUpstreamChecksums(`${ url }.sha256sum`, 'sha256');
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: sidecar[archiveName] },
      });

      return { platform, arch, url, checksum };
    }));
  }
}

export class DockerCLI extends GlobalDependency(GitHubDependency) {
  readonly name = 'dockerCLI';
  readonly githubOwner = 'rancher-sandbox';
  readonly githubRepo = 'rancher-desktop-docker-cli';

  async download(context: DownloadContext): Promise<void> {
    const platform: AssetPlatform = context.dependencyPlatform === 'wsl' ? 'wsl' : context.goPlatform;
    const asset = selectAsset(context, this.name, { platform, arch: context.goArch });
    const destPath = path.join(context.binDir, exeName(context, 'docker'));
    const codesign = context.platform === 'darwin';

    await download(asset.url, destPath, { expectedChecksum: assetChecksum(asset), codesign });
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    const upstream = await fetchUpstreamChecksums(`${ baseURL }/sha256sum.txt`, 'sha256');
    const platforms: readonly AssetPlatform[] = ['linux', 'wsl', 'darwin', 'windows'];

    return Promise.all(cartesian(platforms, ARCHES).map(async([platform, arch]) => {
      const executableName = `docker-${ platform }-${ arch }${ exeSuffix(platform) }`;
      const url = `${ baseURL }/${ executableName }`;
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: upstream[executableName] },
      });

      return { platform, arch, url, checksum };
    }));
  }
}

export class DockerBuildx extends GlobalDependency(GitHubDependency) {
  readonly name = 'dockerBuildx';
  readonly githubOwner = 'docker';
  readonly githubRepo = 'buildx';

  async download(context: DownloadContext): Promise<void> {
    // Download the Docker-Buildx Plug-In
    const asset = selectAsset(context, this.name, { platform: context.goPlatform, arch: context.goArch });
    const dockerBuildxPath = path.join(context.dockerPluginsDir, exeName(context, 'docker-buildx'));

    await download(asset.url, dockerBuildxPath, { expectedChecksum: assetChecksum(asset) });
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    // Upstream checksums.txt omits darwin entries
    // (https://github.com/docker/buildx/issues/945), so we hash darwin without
    // upstream verification.
    const upstream = await fetchUpstreamChecksums(`${ baseURL }/checksums.txt`, 'sha256');

    return Promise.all(cartesian(HOST_PLATFORMS, ARCHES).map(async([platform, arch]) => {
      const executableName = `buildx-v${ version }.${ platform }-${ arch }${ exeSuffix(platform) }`;
      const url = `${ baseURL }/${ executableName }`;
      const verify = platform === 'darwin' ? undefined : { algorithm: 'sha256' as const, expected: upstream[executableName] };
      const checksum = await downloadAndHash(url, verify ? { verify } : undefined);

      return { platform, arch, url, checksum };
    }));
  }
}

export class DockerCompose extends GlobalDependency(GitHubDependency) {
  readonly name = 'dockerCompose';
  readonly githubOwner = 'docker';
  readonly githubRepo = 'compose';

  /** Upstream names compose artifacts with uname-style architectures. */
  private static readonly upstreamArch: Record<GoArch, string> = { amd64: 'x86_64', arm64: 'aarch64' };

  async download(context: DownloadContext): Promise<void> {
    const asset = selectAsset(context, this.name, { platform: context.goPlatform, arch: context.goArch });
    const destPath = path.join(context.dockerPluginsDir, exeName(context, 'docker-compose'));

    await download(asset.url, destPath, { expectedChecksum: assetChecksum(asset) });
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const baseUrl = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;

    return Promise.all(cartesian(HOST_PLATFORMS, ARCHES).map(async([platform, arch]) => {
      const executableName = `docker-compose-${ platform }-${ DockerCompose.upstreamArch[arch] }${ exeSuffix(platform) }`;
      const url = `${ baseUrl }/${ executableName }`;
      const sidecar = await fetchUpstreamChecksums(`${ url }.sha256`, 'sha256');
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: sidecar[executableName] },
      });

      return { platform, arch, url, checksum };
    }));
  }
}

export class GoLangCILint extends GlobalDependency(GitHubDependency) {
  readonly name = 'golangci-lint';
  readonly githubOwner = 'golangci';
  readonly githubRepo = 'golangci-lint';

  download(context: DownloadContext): Promise<void> {
    // We don't actually download anything; when we invoke the linter, we just
    // use `go run` with the appropriate package.
    return Promise.resolve();
  }

  getAssets(version: string): Promise<DependencyAsset[]> {
    return Promise.resolve([]);
  }
}

export class CheckSpelling extends GlobalDependency(GitHubDependency) {
  readonly name = 'check-spelling';
  readonly githubOwner = 'check-spelling';
  readonly githubRepo = 'check-spelling';

  download(context: DownloadContext): Promise<void> {
    // We don't download anything there; `scripts/spelling.sh` does the cloning.
    return Promise.resolve();
  }

  getAssets(version: string): Promise<DependencyAsset[]> {
    return Promise.resolve([]);
  }
}

export class Steve extends GlobalDependency(GitHubDependency) {
  readonly name = 'steve';
  readonly githubOwner = 'rancher-sandbox';
  readonly githubRepo = 'rancher-desktop-steve';
  readonly releaseFilter = 'published-pre';

  async download(context: DownloadContext): Promise<void> {
    const asset = selectAsset(context, this.name, { platform: context.goPlatform, arch: context.goArch });
    const stevePath = path.join(context.internalDir, exeName(context, 'steve'));

    await downloadTarGZ(asset.url, stevePath, { expectedChecksum: assetChecksum(asset) });
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const steveURLBase = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    const upstream = await fetchUpstreamChecksums(`${ steveURLBase }/steve.sha512sum`, 'sha512');
    const archiveMatch = /^steve-(linux|darwin|windows)-(amd64|arm64)\.tar\.gz$/;

    return (await Promise.all(Object.keys(upstream).map(async(archiveName) => {
      const match = archiveMatch.exec(archiveName);

      if (!match) {
        return;
      }
      const [, platform, arch] = match as unknown as [string, AssetPlatform, GoArch];
      const url = `${ steveURLBase }/${ archiveName }`;
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha512', expected: upstream[archiveName] },
      });

      return { platform, arch, url, checksum };
    }))).filter(defined);
  }
}

export class DockerProvidedCredHelpers extends GlobalDependency(GitHubDependency) {
  readonly name = 'dockerProvidedCredentialHelpers';
  readonly githubOwner = 'docker';
  readonly githubRepo = 'docker-credential-helpers';

  /** The credential helpers published for each platform. */
  private static readonly helperNames: Record<AssetPlatform, string[]> = {
    linux:   ['docker-credential-secretservice', 'docker-credential-pass'],
    darwin:  ['docker-credential-osxkeychain'],
    windows: ['docker-credential-wincred'],
    wsl:     [],
  };

  async download(context: DownloadContext): Promise<void> {
    const version = context.dependencies[this.name].version;
    const assets = selectAssets(context, this.name, { platform: context.goPlatform, arch: context.goArch });
    const expected = DockerProvidedCredHelpers.helperNames[context.goPlatform];

    // selectAssets() returns [] on no match, so a missing helper would install silently.
    if (assets.length !== expected.length) {
      throw new Error(
        `Expected ${ expected.length } ${ this.name } assets for ` +
        `${ context.goPlatform }/${ context.goArch }, found ${ assets.length }.`,
      );
    }
    // starting with the 0.7.0 the upstream releases have a broken ad-hoc signature
    const codesign = context.platform === 'darwin';

    await Promise.all(assets.map((asset) => {
      const fullBinName = path.basename(new URL(asset.url).pathname);
      const baseName = fullBinName
        .replace(`-v${ version }.${ context.goPlatform }-${ context.goArch }`, '')
        .replace(/\.exe$/, '');
      const destPath = path.join(context.binDir, exeName(context, baseName));

      return download(asset.url, destPath, { expectedChecksum: assetChecksum(asset), codesign });
    }));
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    const upstream = await fetchUpstreamChecksums(`${ baseURL }/checksums.txt`, 'sha256');
    const matrix: { platform: AssetPlatform, arch: GoArch, baseName: string }[] = [];

    for (const platform of HOST_PLATFORMS) {
      for (const [baseName, arch] of cartesian(DockerProvidedCredHelpers.helperNames[platform], ARCHES)) {
        matrix.push({ platform, arch, baseName });
      }
    }

    return Promise.all(matrix.map(async({ platform, arch, baseName }) => {
      const fullBinName = `${ baseName }-v${ version }.${ platform }-${ arch }${ exeSuffix(platform) }`;
      const url = `${ baseURL }/${ fullBinName }`;
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: upstream[fullBinName] },
      });

      return { platform, arch, url, checksum };
    }));
  }
}

export class ECRCredHelper extends GlobalDependency(GitHubDependency) {
  readonly name = 'ECRCredentialHelper';
  readonly githubOwner = 'awslabs';
  readonly githubRepo = 'amazon-ecr-credential-helper';

  private static readonly baseName = 'docker-credential-ecr-login';
  private static readonly baseUrl = 'https://amazon-ecr-credential-helper-releases.s3.us-east-2.amazonaws.com';

  async download(context: DownloadContext): Promise<void> {
    const asset = selectAsset(context, this.name, { platform: context.goPlatform, arch: context.goArch });
    const destPath = path.join(context.binDir, exeName(context, ECRCredHelper.baseName));

    await download(asset.url, destPath, { expectedChecksum: assetChecksum(asset) });
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    return Promise.all(cartesian(HOST_PLATFORMS, ARCHES).map(async([platform, arch]) => {
      const binName = `${ ECRCredHelper.baseName }${ exeSuffix(platform) }`;
      const url = `${ ECRCredHelper.baseUrl }/${ version }/${ platform }-${ arch }/${ binName }`;
      // Upstream publishes a per-binary `<bin>.sha256` sidecar in GNU format,
      // indexed by the bare binary name without the platform-prefixed path.
      const sidecar = await fetchUpstreamChecksums(`${ url }.sha256`, 'sha256');
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: sidecar[binName] },
      });

      return { platform, arch, url, checksum };
    }));
  }
}
