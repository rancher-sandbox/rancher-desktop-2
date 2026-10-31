import path from 'path';

import {
  AssetPlatform,
  DependencyAsset,
  DownloadContext,
  downloadAndHash,
  fetchUpstreamChecksums,
  GitHubDependency,
  GlobalDependency,
  Platform,
  GoArch,
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

/** The file name suffix for executable files. */
function exeSuffix(platform: AssetPlatform): string {
  return platform === 'windows' ? '.exe' : '';
}

export function cartesian<A extends string, B extends string>(
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
    const arch = context.goArch;
    const asset = selectAsset(context, this.name);

    await downloadTarGZ(asset.url, path.join(context.binDir, exeName(context, 'helm')), {
      expectedChecksum: asset.checksum,
      entryName:        `${ context.goPlatform }-${ arch }/${ exeName(context, 'helm') }`,
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
    const codesign = process.platform === 'darwin';

    await download(asset.url, destPath, { expectedChecksum: asset.checksum, codesign });
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
    const asset = selectAsset(context, this.name);
    const dockerBuildxPath = path.join(context.dockerPluginsDir, exeName(context, 'docker-buildx'));

    await download(asset.url, dockerBuildxPath, { expectedChecksum: asset.checksum });
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    // Docker code-signs the darwin and windows binaries after publishing the
    // release, so checksums.txt omits them.
    // https://github.com/docker/buildx/pull/3978
    const [checksums, signedChecksums] = await Promise.all([
      fetchUpstreamChecksums(`${ baseURL }/checksums.txt`, 'sha256'),
      fetchUpstreamChecksums(`${ baseURL }/checksums-signed.txt`, 'sha256'),
    ]);

    return Promise.all(cartesian(HOST_PLATFORMS, ARCHES).map(async([platform, arch]) => {
      const executableName = `buildx-v${ version }.${ platform }-${ arch }${ exeSuffix(platform) }`;
      const url = `${ baseURL }/${ executableName }`;
      const upstream = ['darwin', 'windows'].includes(platform) ? signedChecksums : checksums;
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: upstream[executableName] },
      });

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
    const asset = selectAsset(context, this.name);
    const destPath = path.join(context.dockerPluginsDir, exeName(context, 'docker-compose'));

    await download(asset.url, destPath, { expectedChecksum: asset.checksum });
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
    const arch = context.goArch;
    const assets = selectAssets(context, this.name);
    const expected = DockerProvidedCredHelpers.helperNames[context.goPlatform];

    // selectAssets() returns [] rather than throwing, so we would silently skip
    // a helper the manifest omits.
    if (assets.length !== expected.length) {
      throw new Error(
        `Expected ${ expected.length } ${ this.name } assets for ` +
        `${ context.goPlatform }/${ arch }, found ${ assets.length }.`,
      );
    }
    // starting with the 0.7.0 the upstream releases have a broken ad-hoc signature
    const codesign = context.platform === 'darwin';

    await Promise.all(assets.map((asset) => {
      const fullBinName = path.basename(new URL(asset.url).pathname);
      const baseName = fullBinName
        .replace(`-v${ version }.${ context.goPlatform }-${ arch }`, '')
        .replace(/\.exe$/, '');
      const destPath = path.join(context.binDir, exeName(context, baseName));

      return download(asset.url, destPath, { expectedChecksum: asset.checksum, codesign });
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

  async download(context: DownloadContext): Promise<void> {
    const asset = selectAsset(context, this.name);
    const destPath = path.join(context.binDir, exeName(context, 'docker-credential-ecr-login'));

    return await download(asset.url, destPath, { expectedChecksum: asset.checksum });
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const baseName = 'docker-credential-ecr-login';
    const baseUrl = 'https://amazon-ecr-credential-helper-releases.s3.us-east-2.amazonaws.com';

    return Promise.all(cartesian(HOST_PLATFORMS, ARCHES).map(async([platform, arch]) => {
      const binName = `${ baseName }${ exeSuffix(platform) }`;
      const url = `${ baseUrl }/${ version }/${ platform }-${ arch }/${ binName }`;
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
