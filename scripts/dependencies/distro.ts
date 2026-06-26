import { cartesian } from '@/scripts/dependencies/tools';
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

/** The image formats the distro ships: ext4 `raw` for Lima, rootfs `tar` for WSL2. */
const FORMATS = ['raw', 'tar'] as const;
const ARCHES: readonly GoArch[] = ['amd64', 'arm64'];

/**
 * The guest Linux distro baked into the rdd binary.  rddepman tracks it like
 * any other GitHub dependency; the Go downloader fetches it at build time
 * (by arch and image format), so there is no host install.
 */
export class Distro extends GlobalDependency(GitHubDependency) {
  readonly name = 'distro';
  readonly githubOwner = 'rancher-sandbox';
  readonly githubRepo = 'rancher-desktop-opensuse';
  readonly manifestPath = GUEST_DEP_VERSIONS_PATH;

  download(_context: DownloadContext): Promise<void> {
    return Promise.reject(new Error('the distro is staged by the build, not by postinstall'));
  }

  async getAssets(version: string): Promise<DependencyAsset[]> {
    const baseURL = `https://github.com/${ this.githubOwner }/${ this.githubRepo }/releases/download/v${ version }`;

    return Promise.all(cartesian(ARCHES, FORMATS).map(async([arch, format]) => {
      const artifact = `distro.v${ version }.${ arch }.${ format }.xz`;
      const url = `${ baseURL }/${ artifact }`;
      // Each artifact has a GNU-format `.sha256` sidecar listing its filename.
      const sidecar = await fetchUpstreamChecksums(`${ url }.sha256`, 'sha256');
      const checksum = await downloadAndHash(url, {
        verify: { algorithm: 'sha256', expected: sidecar[artifact] },
      });

      return {
        platform: 'linux' as const, arch, format, url, checksum,
      };
    }));
  }
}
