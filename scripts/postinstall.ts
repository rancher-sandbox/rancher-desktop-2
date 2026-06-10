import os from 'os';
import path from 'path';

import { Electron } from '@/scripts/dependencies/electron';
import { RDD } from '@/scripts/dependencies/rdd';
import * as tools from '@/scripts/dependencies/tools';
import { Wix } from '@/scripts/dependencies/wix';
import buildUtils from '@/scripts/lib/build-utils';
import {
  Dependency,
  DependencyManifest,
  DependencyPlatform,
  DownloadContext,
  readDependencyManifest,
} from '@/scripts/lib/dependencies';
import { simpleSpawn } from '@/scripts/simple_process';

interface DependencyWithContext {
  dependency: Dependency;
  context:    DownloadContext;
}

/**
 * The amount of time we allow the post-install script to run, in milliseconds.
 */
const InstallTimeout = 10 * 60 * 1_000; // Ten minutes.

// Dependencies that should be installed into places that users touch
// (so users' WSL distros and hosts as of the time of writing).
const userTouchedDependencies: Dependency[] = [
  new tools.Helm(),
  new tools.DockerCLI(),
  new tools.DockerBuildx(),
  new tools.DockerCompose(),
  new tools.DockerProvidedCredHelpers(),
  new tools.ECRCredHelper(),
];

// Dependencies that are specific to unix hosts.
const unixDependencies: Dependency[] = [];

// Dependencies that are specific to macOS hosts.
const macOSDependencies: Dependency[] = [];

// Dependencies that are specific to windows hosts.
const windowsDependencies = [
  new Wix(),
];

// Dependencies that are specific to WSL.
const wslDependencies: Dependency[] = [];

// Dependencies that are specific to WSL and Lima VMs.
const vmDependencies: Dependency[] = [];

// Dependencies that are specific to hosts.
const hostDependencies = [
  new tools.Steve(),
  new Electron(),
  new RDD(),
];

async function downloadDependencies(items: DependencyWithContext[]): Promise<void> {
  function specialize(item: DependencyWithContext) {
    return `${ item.dependency.name }:${ item.context.platform }`;
  }
  // Dependencies might depend on other dependencies.  Note that we may have
  // multiple dependencies of the same name, but different platforms; therefore,
  // all dependencies are keyed by <name>:<platform>.
  const dependenciesByName = Object.fromEntries(items.map(item => [specialize(item), item]));
  const forwardDependencies = Object.fromEntries(items.map(item => [specialize(item), [] as string[]] as const));
  const reverseDependencies = Object.fromEntries(items.map(item => [specialize(item), [] as string[]] as const));
  const all = new Set(Object.keys(dependenciesByName));
  const running = new Set<string>();
  const done = new Set<string>();
  const promises: Record<string, Promise<void>> = {};

  for (const item of items) {
    const dependencies = item.dependency.dependencies?.(item.context) ?? [];

    forwardDependencies[specialize(item)].push(...dependencies);
    for (const dependency of dependencies) {
      if (dependency in reverseDependencies) {
        reverseDependencies[dependency].push(specialize(item));
      } else {
        throw new Error(`Dependency ${ item.dependency.name } depends on unknown dependency ${ dependency }`);
      }
    }
  }
  async function process(name: string) {
    running.add(name);
    const item = dependenciesByName[name];

    await item.dependency.download(item.context);
    done.add(name);
    for (const dependent of reverseDependencies[name]) {
      if (!running.has(dependent)) {
        if (forwardDependencies[dependent].every(d => done.has(d))) {
          promises[dependent] = process(dependent);
        }
      }
    }
  }

  for (const item of items.filter(d => (d.dependency.dependencies?.(d.context) ?? []).length === 0)) {
    promises[specialize(item)] = process(specialize(item));
  }

  const abortSignal = AbortSignal.timeout(InstallTimeout);

  while (!abortSignal.aborted && running.size > done.size) {
    const timeout = new Promise((resolve) => {
      setTimeout(resolve, 60_000);
      abortSignal.onabort = resolve;
    });
    const pending = Array.from(running).filter(v => !done.has(v));

    await Promise.race([timeout, ...pending.map(v => promises[v])]);
  }
  abortSignal.onabort = null;

  if (all.size > done.size) {
    const remaining = Array.from(all).filter(d => !done.has(d)).sort();
    const message = [`${ remaining.length } dependencies are stuck:`];

    for (const key of remaining) {
      const deps = forwardDependencies[key].filter(d => !done.has(d));
      const depsString = deps.length > 0 ? deps.join(', ') : '(nothing)';
      const started = running.has(key) ? ' (started)' : '';

      message.push(`    ${ key }${ started } depends on ${ depsString }`);
    }
    if (abortSignal.aborted) {
      message.unshift('Timed out downloading dependencies');
    }
    throw new Error(message.join('\n'));
  }
}

async function runScripts(): Promise<void> {
  // load desired versions and checksums of dependencies
  const manifest = await readDependencyManifest(path.join('pkg', 'rancher-desktop', 'assets', 'dependencies.yaml'));
  const platform = os.platform();
  const dependencies: DependencyWithContext[] = [];

  if (platform === 'linux' || platform === 'darwin') {
    // download things that go on unix host
    const hostDownloadContext = await buildDownloadContextFor(platform, manifest);

    for (const dependency of [...userTouchedDependencies, ...unixDependencies, ...hostDependencies]) {
      dependencies.push({ dependency, context: hostDownloadContext });
    }

    // download things for macOS host
    if (platform === 'darwin') {
      for (const dependency of macOSDependencies) {
        dependencies.push({ dependency, context: hostDownloadContext });
      }
    }

    // download things that go inside Lima VM
    const vmDownloadContext = await buildDownloadContextFor('linux', manifest);

    dependencies.push(...vmDependencies.map(dependency => ({ dependency, context: vmDownloadContext })));
  } else if (platform === 'win32') {
    // download things for windows
    const hostDownloadContext = await buildDownloadContextFor('win32', manifest);

    for (const dependency of [...userTouchedDependencies, ...windowsDependencies, ...hostDependencies]) {
      dependencies.push({ dependency, context: hostDownloadContext });
    }

    // download things that go inside WSL distro
    const vmDownloadContext = await buildDownloadContextFor('wsl', manifest);

    for (const dependency of [...userTouchedDependencies, ...wslDependencies, ...vmDependencies]) {
      dependencies.push({ dependency, context: vmDownloadContext });
    }
  }

  await downloadDependencies(dependencies);
}

function buildDownloadContextFor(rawPlatform: DependencyPlatform, manifest: DependencyManifest): Promise<DownloadContext> {
  const platform = rawPlatform === 'wsl' ? 'linux' : rawPlatform;
  const arch = buildUtils.arch;
  const goArch = arch === 'arm64' ? 'arm64' : 'amd64';
  const resourcesDir = path.join(process.cwd(), 'resources');
  return Promise.resolve({
    dependencies:       manifest,
    dependencyPlatform: rawPlatform,
    platform,
    arch,
    goPlatform:         platform === 'win32' ? 'windows' : platform,
    goArch,
    resourcesDir,
    binDir:             path.join(resourcesDir, platform, 'bin'),
    internalDir:        path.join(resourcesDir, 'internal'),
    dockerPluginsDir:   path.join(resourcesDir, platform, 'docker-cli-plugins'),
    hostDir:            path.join(resourcesDir, 'host'),
  });
}

// The main purpose of this setTimeout is to keep the script waiting until the main async function finishes
const keepScriptAlive = setTimeout(() => { }, 24 * 3600 * 1000);

(async() => {
  let exitCode = 2;

  try {
    await runScripts();
    await simpleSpawn('node',
      ['node_modules/electron-builder/out/cli/cli.js', 'install-app-deps']);
    exitCode = 0;
  } catch (e: any) {
    console.error('POSTINSTALL ERROR: ', e);
  } finally {
    clearTimeout(keepScriptAlive);
    process.exit(exitCode);
  }
})();
