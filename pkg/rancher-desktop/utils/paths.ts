/**
 * This module describes the various paths we use to store state & data.
 */
import { spawnSync } from 'child_process';
import fs from 'fs';
import os from 'os';
import path from 'path';

import electron from 'electron';
import which from 'which';

/**
 * RDDPaths are the paths provided by the RDD daemon.  These should be kept in
 * sync with the output of `rdd service paths`.
 */
interface RDDPaths {
  /** RDD: file holding server arguments. */
  readonly args_file:     string;
  /** RDD: Kubernetes connection configuration file. */
  readonly config:        string;
  /** RDD: application data directory. */
  readonly dir:           string;
  /** RDD: Docker socket file. */
  readonly docker_socket: string;
  /** RDD: Lima home directory. */
  readonly lima_home:     string;
  /** RDD: log directory. */
  readonly log_dir:       string;
  /** RDD: PID file. */
  readonly pid_file:      string;
  /** RDD: short user data directory. */
  readonly short_dir:     string;
  /** RDD: TLS certificate directory. */
  readonly tls_dir:       string;
}

/**
 * RDAPaths are the paths specific to Rancher Desktop App.
 */
interface RDAPaths {
  /** RDA: path to the RDD executable. */
  readonly rdd:       string;
  /** RDA: resources directory. */
  readonly resources: string;
  /** RDA: cache directory. */
  readonly cache:     string;
}

export interface Paths extends RDDPaths, RDAPaths { }

let cachedRDDPath: string | undefined;

/**
 * Get the path to the RDD executable.
 * @param useCached Use the cached path, if available.
 */
export function getRDDPath(useCached = true): string {
  const exeName = process.platform === 'win32' ? 'rdd.exe' : 'rdd';

  if (useCached && cachedRDDPath) {
    return cachedRDDPath;
  }

  cachedRDDPath = (() => {
    if (electron.app?.isPackaged) {
      const packagedPath = path.join(
        process.resourcesPath, 'resources', process.platform, 'bin', exeName);
      try {
        fs.accessSync(packagedPath, fs.constants.X_OK);
        return packagedPath;
      } catch { /* ignore */ }
    }

    const relativePath = path.join(
      process.cwd(), 'resources', process.platform, 'bin', exeName);
    try {
      fs.accessSync(relativePath, fs.constants.X_OK);
      return relativePath;
    } catch { /* ignore */ }

    return which.sync(exeName, { nothrow: true }) ?? undefined;
  })();

  if (!cachedRDDPath) {
    throw new Error(`Unable to find Rancher Desktop Daemon executable (${ exeName })`);
  }
  return cachedRDDPath;
}

type PlatformSpecificPaths = Pick<RDAPaths, 'cache'>;
type PlatformAgnosticPaths = Pick<RDAPaths, 'resources'>;

/**
 * TEST_PLATFORM is a special platform name for testing.
 */
export const TEST_PLATFORM = Symbol('test-platform');

type supportedPlatforms = 'darwin' | 'linux' | 'win32' | typeof TEST_PLATFORM;

class UnsupportedPlatformError extends Error {
  constructor(platform: supportedPlatforms) {
    super(`Unsupported platform: ${ String(platform) }`);
    this.name = UnsupportedPlatformError.name;
  }
}

/**
 * platformSpecificPaths contains the RDA paths that are specific to each
 * platform.
 */
function getPlatformSpecificPaths(rdd: RDDPaths, platform: supportedPlatforms): PlatformSpecificPaths {
  // Return getters here so they can be lazy-evaluated, which allows us to avoid
  // overriding them in tests when we never touch the values.
  return ({
    darwin: {
      get cache() {
        return path.join(os.homedir(), 'Library', 'Caches', path.basename(rdd.dir));
      },
    },
    linux: {
      get cache() {
        return path.join(os.homedir(), '.cache', path.basename(rdd.dir));
      },
    },
    win32: {
      get cache() {
        return path.join(rdd.dir, 'cache');
      },
    },
    [TEST_PLATFORM]: {
      get cache() {
        return path.join(rdd.dir, 'cache');
      },
    },
  } satisfies Record<supportedPlatforms, PlatformSpecificPaths>)[platform];
};

/**
 * Paths which do not depend on the platform.
 */
const platformAgnosticPaths: PlatformAgnosticPaths = {
  // Use a getter here so we can override `NODE_ENV` and `process.resourcesPath` in tests.
  get resources() {
    if (process.env.NODE_ENV === 'production') {
      return process.resourcesPath;
    }
    // When under test, we do not have `process.resourcesPath`; use a fallback.
    return process.resourcesPath ?? path.join(process.cwd(), 'resources');
  },
};

/**
 * Fallback paths, in case we are unable to find them (e.g. we're running build
 * scripts).
 * @note These are not used in production.
 * @returns A mapping of path names to functions that return the path (where
 * callbacks exist); if the platform is unsupported, throws an error.
 */
function getPlatformSpecificFallbacks(platform: supportedPlatforms): Partial<Record<keyof Paths, () => string>> {
  // For some of the paths, we just tack the property name to the `dir` path.
  const getters: { dir: () => string } & Partial<Record<keyof Paths, () => string>> = ({
    darwin: {
      dir() {
        return path.join(os.homedir(), 'Library', 'Application Support', 'rancher-desktop');
      },
      cache() {
        return path.join(os.homedir(), 'Library', 'Caches', 'rancher-desktop');
      },
      log_dir() {
        return path.join(os.homedir(), 'Library', 'Logs', 'rancher-desktop');
      },
    },
    linux: {
      dir() {
        return path.join(os.homedir(), '.local', 'share', 'rancher-desktop');
      },
      cache() {
        return path.join(os.homedir(), '.cache', 'rancher-desktop');
      },
    },
    win32: {
      dir() {
        return path.join(process.env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local'), 'rancher-desktop');
      },
    },
    [TEST_PLATFORM]: {
      dir() {
        return path.join(os.tmpdir(), 'rancher-desktop');
      },
    },
  } satisfies Record<supportedPlatforms, Partial<Record<keyof Paths, () => string>>>)[platform];

  if (!getters) {
    throw new UnsupportedPlatformError(platform);
  }

  const fallbackKeys = ['cache', 'log_dir'] as const;
  for (const key of fallbackKeys) {
    getters[key as keyof typeof getters] ||= () => {
      return path.join(getters.dir(), key);
    };
  }

  return getters;
}

/**
 * Get the paths used by Rancher Desktop.
 * @note This is only exported for testing; consumers should use the default export.
 * @param rddOverride The path to the RDD executable; if not provided, it will
 *                    be determined automatically.
 * @throws If the platform is unsupported.
 */
export function getPaths(rddOverride?: string, platformOverride?: supportedPlatforms): Paths {
  try {
    const rddPath = rddOverride ?? getRDDPath();
    const rawRDD = spawnSync(
      rddPath, ['service', 'paths', '--output=json'], { encoding: 'utf8', windowsHide: true });
    if (rawRDD.error) {
      // spawnSync returns the error in a property instead of throwing.
      throw rawRDD.error;
    }
    if (rawRDD.status !== 0) {
      throw new Error(`RDD process exited with code ${ rawRDD.status }: ${ rawRDD.stderr }`);
    }
    const rddPaths = JSON.parse(rawRDD.stdout) as RDDPaths;
    const platform = platformOverride ?? process.platform as supportedPlatforms;
    const platformSpecificPaths = getPlatformSpecificPaths(rddPaths, platform);

    if (!platformSpecificPaths) {
      throw new UnsupportedPlatformError(platform);
    }

    // Return the object using property descriptors to avoid evaluating getters
    // too early.
    return Object.defineProperties({} as any, {
      ...Object.getOwnPropertyDescriptors(rddPaths),
      ...Object.getOwnPropertyDescriptors(platformAgnosticPaths),
      ...Object.getOwnPropertyDescriptors(platformSpecificPaths),
      rdd: { value: rddPath, enumerable: true },
    } satisfies { [K in keyof Paths]: PropertyDescriptor });
  } catch (cause) {
    if (cause instanceof UnsupportedPlatformError) {
      throw cause;
    }
    // Delay throwing the error until we actually access the paths; this way we
    // can track down where we actually use the path, as well as handle fallbacks.
    return new Proxy({} as Paths, {
      get(target, prop) {
        if (typeof prop !== 'string' || prop === 'then' || prop in Object.prototype) {
          // Do not handle these; they are not things we expect in paths.
          return Reflect.get(target, prop);
        }
        if (prop === 'rdd' && rddOverride) {
          // If there's an override for RDD, use all the time.  However, do not
          // try to find it otherwise, as that may be the cause of the error.
          return rddOverride;
        }
        if (prop in platformAgnosticPaths) {
          return platformAgnosticPaths[prop as keyof PlatformAgnosticPaths];
        }
        if (process.env.NODE_ENV !== 'production') {
          const platform = platformOverride ?? process.platform as supportedPlatforms;
          const fallbacks = getPlatformSpecificFallbacks(platform);
          if (fallbacks?.[prop as keyof Paths]) {
            return fallbacks[prop as keyof Paths]?.();
          }
        }
        throw new Error(`Unable to determine path for ${ String(prop) }: ${ String(cause) }`, { cause });
      },
    });
  }
}

export default getPaths();
