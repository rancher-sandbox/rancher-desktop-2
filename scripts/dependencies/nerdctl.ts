import {
  DependencyAsset,
  DownloadContext,
  downloadAndHash,
  fetchUpstreamChecksums,
  GitHubDependency,
  GlobalDependency,
  GoArch,
  GUEST_DEP_VERSIONS_PATH,
} from '@/scripts/lib/dependencies';

const ARCHES: readonly GoArch[] = ['amd64', 'arm64'];

/**
 * nerdctl, which the distro overlay will bake into the guest image.  rddepman
 * tracks it like any other GitHub dependency, so there is no host install.
 * The Go downloader stages only the distro until the overlay work lands.
 */
export class Nerdctl extends GlobalDependency(GitHubDependency) {
  readonly name = 'nerdctl';
  readonly githubOwner = 'containerd';
  readonly githubRepo = 'nerdctl';
  readonly manifestPath = GUEST_DEP_VERSIONS_PATH;

  download(_context: DownloadContext): Promise<void> {
    return Promise.reject(new Error('nerdctl is a guest-only dependency and is not installed by postinstall'));
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;
    const upstream = await fetchUpstreamChecksums(`${ baseURL }/SHA256SUMS`, 'sha256');

    return Promise.all(ARCHES.map(async(arch) => {
      const artifact = `nerdctl-${ version }-linux-${ arch }.tar.gz`;
      const url = `${ baseURL }/${ artifact }`;
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: upstream[artifact] },
      });

      return { platform: 'linux' as const, arch, url, checksum };
    }));
  }
}
