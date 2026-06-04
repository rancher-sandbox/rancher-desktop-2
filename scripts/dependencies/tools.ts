import path from 'path';

import { defined } from '@/pkg/rancher-desktop/utils/typeUtils';
import {
  DownloadContext,
  downloadAndHash,
  fetchUpstreamChecksums,
  GitHubDependency,
  GlobalDependency,
  lookupChecksum,
  Sha256Checksum,
  Platform,
} from '@/scripts/lib/dependencies';
import {
  download,
  downloadTarGZ,
} from '@/scripts/lib/download';

function exeName(contextOrPlatform: DownloadContext | Platform | 'windows', name: string) {
  const platform = typeof contextOrPlatform === 'string' ? contextOrPlatform : contextOrPlatform.platform;
  return `${ name }${ platform.startsWith('win') ? '.exe' : '' }`;
}

export function cartesian<A, B>(
  as: readonly A[],
  bs: readonly B[],
): [A, B][] {
  return as.flatMap(a => bs.map<[A, B]>(b => [a, b]));
}
export class Helm extends GlobalDependency(GitHubDependency) {
  readonly name = 'helm';
  readonly githubOwner = 'helm';
  readonly githubRepo = 'helm';

  async download(context: DownloadContext): Promise<void> {
    // Download Helm. It is a tar.gz file that needs to be expanded and file moved.
    const arch = context.isM1 ? 'arm64' : 'amd64';
    const archiveName = `helm-v${ context.dependencies.helm.version }-${ context.goPlatform }-${ arch }.tar.gz`;
    const helmURL = `https://get.helm.sh/${ archiveName }`;

    await downloadTarGZ(helmURL, path.join(context.binDir, exeName(context, 'helm')), {
      expectedChecksum: lookupChecksum(context, this.name, archiveName),
      entryName:        `${ context.goPlatform }-${ arch }/${ exeName(context, 'helm') }`,
    });
  }

  async getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    const platforms = cartesian(['linux', 'darwin', 'windows'], ['amd64', 'arm64']);

    return Object.fromEntries(await Promise.all(platforms.map(async([goPlatform, arch]) => {
      const archiveName = `helm-v${ version }-${ goPlatform }-${ arch }.tar.gz`;
      const url = `https://get.helm.sh/${ archiveName }`;
      // Helm publishes a sidecar `.sha256sum` per artifact, one line of `<hex>  <filename>`.
      const sidecar = await fetchUpstreamChecksums(`${ url }.sha256sum`, 'sha256');
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: sidecar[archiveName] },
      });

      return [archiveName, checksum];
    })));
  }
}

export class DockerCLI extends GlobalDependency(GitHubDependency) {
  readonly name = 'dockerCLI';
  readonly githubOwner = 'rancher-sandbox';
  readonly githubRepo = 'rancher-desktop-docker-cli';

  async download(context: DownloadContext): Promise<void> {
    const dockerPlatform = context.dependencyPlatform === 'wsl' ? 'wsl' : context.goPlatform;
    const arch = context.isM1 ? 'arm64' : 'amd64';
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ context.dependencies.dockerCLI.version }`;
    const executableName = exeName(context, `docker-${ dockerPlatform }-${ arch }`);
    const dockerURL = `${ baseURL }/${ executableName }`;
    const destPath = path.join(context.binDir, exeName(context, 'docker'));
    const expectedChecksum = lookupChecksum(context, this.name, executableName);
    const codesign = context.platform === 'darwin';

    await download(dockerURL, destPath, { expectedChecksum, codesign });
  }

  async getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    const upstream = await fetchUpstreamChecksums(`${ baseURL }/sha256sum.txt`, 'sha256');
    const platforms = cartesian(['linux', 'wsl', 'darwin', 'windows'], ['amd64', 'arm64']);

    return Object.fromEntries(await Promise.all(platforms.map(async([dockerPlatform, arch]) => {
      const executableName = `docker-${ dockerPlatform }-${ arch }` + (dockerPlatform === 'windows' ? '.exe' : '');
      const checksum = await downloadAndHash(`${ baseURL }/${ executableName }`, {
        verify: { algorithm: 'sha256', expected: upstream[executableName] },
      });

      return [executableName, checksum];
    })));
  }
}

export class DockerBuildx extends GlobalDependency(GitHubDependency) {
  readonly name = 'dockerBuildx';
  readonly githubOwner = 'docker';
  readonly githubRepo = 'buildx';

  async download(context: DownloadContext): Promise<void> {
    // Download the Docker-Buildx Plug-In
    const arch = context.isM1 ? 'arm64' : 'amd64';
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ context.dependencies.dockerBuildx.version }`;
    const executableName = exeName(context, `buildx-v${ context.dependencies.dockerBuildx.version }.${ context.goPlatform }-${ arch }`);
    const dockerBuildxURL = `${ baseURL }/${ executableName }`;
    const dockerBuildxPath = path.join(context.dockerPluginsDir, exeName(context, 'docker-buildx'));
    const expectedChecksum = lookupChecksum(context, this.name, executableName);

    await download(dockerBuildxURL, dockerBuildxPath, { expectedChecksum });
  }

  async getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    // Upstream checksums.txt omits darwin entries
    // (https://github.com/docker/buildx/issues/945), so we hash darwin without
    // upstream verification.
    const upstream = await fetchUpstreamChecksums(`${ baseURL }/checksums.txt`, 'sha256');
    const platforms = cartesian(['linux', 'darwin', 'windows'], ['amd64', 'arm64']);

    return Object.fromEntries(await Promise.all(platforms.map(async([goPlatform, arch]) => {
      const executableName = `buildx-v${ version }.${ goPlatform }-${ arch }` + (goPlatform === 'windows' ? '.exe' : '');
      const url = `${ baseURL }/${ executableName }`;
      const verify = goPlatform === 'darwin' ? undefined : { algorithm: 'sha256' as const, expected: upstream[executableName] };
      const checksum = await downloadAndHash(url, verify ? { verify } : undefined);

      return [executableName, checksum];
    })));
  }
}

export class DockerCompose extends GlobalDependency(GitHubDependency) {
  readonly name = 'dockerCompose';
  readonly githubOwner = 'docker';
  readonly githubRepo = 'compose';

  async download(context: DownloadContext): Promise<void> {
    const baseUrl = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ context.dependencies.dockerCompose.version }`;
    const arch = context.isM1 ? 'aarch64' : 'x86_64';
    const executableName = exeName(context, `docker-compose-${ context.goPlatform }-${ arch }`);
    const url = `${ baseUrl }/${ executableName }`;
    const destPath = path.join(context.dockerPluginsDir, exeName(context, 'docker-compose'));
    const expectedChecksum = lookupChecksum(context, this.name, executableName);

    await download(url, destPath, { expectedChecksum });
  }

  async getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    const baseUrl = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    const platforms = cartesian(['linux', 'darwin', 'windows'], ['x86_64', 'aarch64']);

    return Object.fromEntries(await Promise.all(platforms.map(async([goPlatform, arch]) => {
      const executableName = `docker-compose-${ goPlatform }-${ arch }` + (goPlatform === 'windows' ? '.exe' : '');
      const url = `${ baseUrl }/${ executableName }`;
      const sidecar = await fetchUpstreamChecksums(`${ url }.sha256`, 'sha256');
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: sidecar[executableName] },
      });

      return [executableName, checksum];
    })));
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

  getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    return Promise.resolve({});
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

  getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    return Promise.resolve({});
  }
}

export class Steve extends GlobalDependency(GitHubDependency) {
  readonly name = 'steve';
  readonly githubOwner = 'rancher-sandbox';
  readonly githubRepo = 'rancher-desktop-steve';
  readonly releaseFilter = 'published-pre';

  async download(context: DownloadContext): Promise<void> {
    const steveURLBase = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ context.dependencies.steve.version }`;
    const arch = context.isM1 ? 'arm64' : 'amd64';
    const archiveName = `steve-${ context.goPlatform }-${ arch }.tar.gz`;
    const steveURL = `${ steveURLBase }/${ archiveName }`;
    const stevePath = path.join(context.internalDir, exeName(context, 'steve'));
    const expectedChecksum = lookupChecksum(context, this.name, archiveName);

    await downloadTarGZ(steveURL, stevePath, { expectedChecksum });
  }

  async getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    const steveURLBase = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    const upstream = await fetchUpstreamChecksums(`${ steveURLBase }/steve.sha512sum`, 'sha512');
    const archiveMatch = /^steve-(linux|darwin|windows)-(amd64|arm64)\.tar\.gz$/;

    return Object.fromEntries((await Promise.all(Object.keys(upstream).map(async(archiveName) => {
      if (!archiveMatch.test(archiveName)) {
        return;
      }

      const url = `${ steveURLBase }/${ archiveName }`;
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha512', expected: upstream[archiveName] },
      });

      return [archiveName, checksum] as const;
    }))).filter(defined));
  }
}

export class DockerProvidedCredHelpers extends GlobalDependency(GitHubDependency) {
  readonly name = 'dockerProvidedCredentialHelpers';
  readonly githubOwner = 'docker';
  readonly githubRepo = 'docker-credential-helpers';

  async download(context: DownloadContext): Promise<void> {
    const arch = context.isM1 ? 'arm64' : 'amd64';
    const version = context.dependencies.dockerProvidedCredentialHelpers.version;
    const credHelperNames = {
      linux:  ['docker-credential-secretservice', 'docker-credential-pass'],
      darwin: ['docker-credential-osxkeychain'],
      win32:  ['docker-credential-wincred'],
    }[context.platform];
    const promises = [];
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;

    for (const baseName of credHelperNames) {
      const fullBaseName = `${ baseName }-v${ version }.${ context.goPlatform }-${ arch }`;
      const fullBinName = exeName(context, fullBaseName);
      const sourceURL = `${ baseURL }/${ fullBinName }`;
      const expectedChecksum = lookupChecksum(context, this.name, fullBinName);
      const binName = exeName(context, baseName);
      const destPath = path.join(context.binDir, binName);
      // starting with the 0.7.0 the upstream releases have a broken ad-hoc signature
      const codesign = context.platform === 'darwin';

      promises.push(download(sourceURL, destPath, { expectedChecksum, codesign } ));
    }

    await Promise.all(promises);
  }

  async getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    const upstream = await fetchUpstreamChecksums(`${ baseURL }/checksums.txt`, 'sha256');
    const credHelperNames = {
      linux:   ['docker-credential-secretservice', 'docker-credential-pass'],
      darwin:  ['docker-credential-osxkeychain'],
      windows: ['docker-credential-wincred'],
    } satisfies Record<string, string[]>;
    const matrix: { goPlatform: keyof typeof credHelperNames, arch: string, baseName: string }[] = [];

    for (const [goPlatform, names] of Object.entries(credHelperNames)) {
      for (const [baseName, arch] of cartesian(names, ['amd64', 'arm64'])) {
        matrix.push({ goPlatform: goPlatform as keyof typeof credHelperNames, arch, baseName });
      }
    }

    return Object.fromEntries(await Promise.all(matrix.map(async({ goPlatform, arch, baseName }) => {
      const fullBaseName = `${ baseName }-v${ version }.${ goPlatform }-${ arch }`;
      const fullBinName = exeName(goPlatform, fullBaseName);
      const checksum = await downloadAndHash(`${ baseURL }/${ fullBinName }`, {
        verify: { algorithm: 'sha256', expected: upstream[fullBinName] },
      });

      return [fullBinName, checksum];
    })));
  }
}

export class ECRCredHelper extends GlobalDependency(GitHubDependency) {
  readonly name = 'ECRCredentialHelper';
  readonly githubOwner = 'awslabs';
  readonly githubRepo = 'amazon-ecr-credential-helper';

  async download(context: DownloadContext): Promise<void> {
    const arch = context.isM1 ? 'arm64' : 'amd64';
    const ecrLoginPlatform = context.platform.startsWith('win') ? 'windows' : context.platform;
    const baseName = 'docker-credential-ecr-login';
    const baseUrl = 'https://amazon-ecr-credential-helper-releases.s3.us-east-2.amazonaws.com';
    const binName = exeName(context, baseName);
    const sourceUrl = `${ baseUrl }/${ context.dependencies.ECRCredentialHelper.version }/${ ecrLoginPlatform }-${ arch }/${ binName }`;
    const destPath = path.join(context.binDir, binName);
    const expectedChecksum = lookupChecksum(context, this.name, `${ ecrLoginPlatform }-${ arch }/${ binName }`);

    return await download(sourceUrl, destPath, { expectedChecksum });
  }

  async getChecksums(version: string): Promise<Record<string, Sha256Checksum>> {
    const baseName = 'docker-credential-ecr-login';
    const baseUrl = 'https://amazon-ecr-credential-helper-releases.s3.us-east-2.amazonaws.com';
    const platforms = cartesian(['linux', 'darwin', 'windows'] as const, ['amd64', 'arm64'] as const);

    return Object.fromEntries(await Promise.all(platforms.map(async([ecrLoginPlatform, arch]) => {
      const binName = exeName(ecrLoginPlatform, baseName);
      const key = `${ ecrLoginPlatform }-${ arch }/${ binName }`;
      const url = `${ baseUrl }/${ version }/${ key }`;
      // Upstream publishes a per-binary `<bin>.sha256` sidecar in GNU format,
      // indexed by the bare binary name without the platform-prefixed path.
      const sidecar = await fetchUpstreamChecksums(`${ url }.sha256`, 'sha256');
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: sidecar[binName] },
      });

      return [key, checksum];
    })));
  }
}
