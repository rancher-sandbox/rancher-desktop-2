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
} from '@/scripts/lib/dependencies';
import {
  downloadTarGZ,
} from '@/scripts/lib/download';

function exeName(context: DownloadContext, name: string) {
  const onWindows = context.platform === 'win32';

  return `${ name }${ onWindows ? '.exe' : '' }`;
}

export function cartesian<A extends string, B extends string>(
  as: readonly A[],
  bs: readonly B[],
): [A, B][] {
  return as.flatMap(a => bs.map<[A, B]>(b => [a, b]));
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
