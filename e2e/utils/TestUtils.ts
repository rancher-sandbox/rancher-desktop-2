/**
 * TestUtils exports functions required for the E2E test specs.
 */
import childProcess from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import util from 'node:util';

import { expect, _electron, ElectronApplication, TestInfo } from '@playwright/test';
import _, { GetFieldType } from 'lodash';
import { Page } from 'playwright-core';

import packageMeta from '@/package.json' with { type: 'json' };
import { defaultSettings, Settings } from '@pkg/config/settings';
import { spawnFile } from '@pkg/utils/childProcess';
import paths from '@pkg/utils/paths';
import { RecursivePartial, RecursiveTypes } from '@pkg/utils/typeUtils';

let currentTest: undefined | {
  file:            string,
  startTime:       number,
  options:         startRancherDesktopOptions,
  mockController?: childProcess.ChildProcess;
};

/**
 * Create empty default settings to bypass gracefully
 * FirstPage window.
 */
export function createDefaultSettings(overrides: RecursivePartial<Settings> = {}) {
  const defaultOverrides: RecursivePartial<Settings> = {
    kubernetes:  { enabled: true },
    application: {
      debug:                  true,
      startInBackground:      false,
    },
    virtualMachine: { memoryInGB: 2 },
  };
  const settingsData: Settings = _.merge({}, defaultSettings, defaultOverrides, overrides);

  const settingsJson = JSON.stringify(settingsData);
  const fileSettingsName = 'settings.json';
  const settingsFullPath = path.join(paths.config, fileSettingsName);

  if (!fs.existsSync(settingsFullPath)) {
    fs.mkdirSync(paths.config, { recursive: true });
    fs.writeFileSync(path.join(paths.config, fileSettingsName), settingsJson);
    console.log(`Default settings file successfully created at ${ paths.config }/${ fileSettingsName }`);
  } else {
    try {
      const contents = fs.readFileSync(settingsFullPath, { encoding: 'utf-8' });
      const settings: Settings = JSON.parse(contents.toString());
      const desiredSettings: Settings = _.merge({}, settings, defaultOverrides, overrides);

      if (!_.eq(settings, desiredSettings)) {
        fs.writeFileSync(settingsFullPath, JSON.stringify(desiredSettings), { encoding: 'utf-8' });
      }
    } catch (err) {
      console.log(`Failed to process ${ settingsFullPath }: ${ err }`);
    }
  }
}

/**
 * getAlternateSetting returns the setting that isn't the same as the existing setting.
 */
export function getAlternateSetting<K extends keyof RecursiveTypes<Settings>>(currentSettings: Settings, setting: K, altOne: GetFieldType<Settings, K>, altTwo: GetFieldType<Settings, K>) {
  return _.get(currentSettings, setting) === altOne ? altTwo : altOne;
}

/**
 * Calculate the path of an asset that should be attached to a test run.
 * @param type What kind of asset this is:
 * - 'trace' (default): Playwright trace file.
 * - 'log': The directory where logs are stored.
 * - 'instance': RDD instance suffix.
 */
export function reportAsset(testInfo: TestInfo, type: 'trace' | 'log' | 'instance' = 'trace') {
  const testName = testInfo.file;
  let name = `${ path.basename(testName).replace(/(?:\.e2e)(?:\.spec)(?:\.ts)$/, '') }-`;

  if (currentTest?.options?.logVariant) {
    name += `${ currentTest.options.logVariant }-`;
  }
  if (testInfo.retry) {
    name += `try-${ testInfo.retry }-`;
  }
  name += {
    trace:    'pw-trace.zip',
    log:      'logs',
    instance: '',
  }[type];

  if (type === 'instance') {
    return 'e2e-' + name.replace(/-$/, '');
  }

  return path.join(import.meta.dirname, '..', 'reports', name);
}

/**
 * Tear down the application, without managing logging.  This should only be
 * used when doing atypical tests that need to restart the application within
 * the test.  This is normally used instead of `app.close()`.
 *
 * @note teardown() should be used where possible.
 */
export async function teardownApp(app: ElectronApplication) {
  const proc = app.process();
  const pid = proc.pid;

  try {
    // Allow one minute for shutdown
    await Promise.race([
      app.close(),
      util.promisify(setTimeout)(60 * 1000),
    ]);
  } finally {
    if (proc.kill('SIGTERM') || proc.kill('SIGKILL')) {
      console.log(`Manually stopped process ${ pid }`);
    }
    // Try to do platform-specific killing based on process groups
    if (process.platform === 'darwin' || process.platform === 'linux') {
      // Send SIGTERM to the process group, wait three seconds, then send
      // SIGKILL and wait for one more second.
      for (const [signal, timeout] of [['TERM', 3_000], ['KILL', 1_000]] as const) {
        let pids: string[];

        try {
          const args = ['-o', 'pid=', process.platform === 'darwin' ? '-g' : '--sid', `${ pid }`];
          const { stdout } = await spawnFile('ps', args, { stdio: ['ignore', 'pipe', 'inherit'] });

          pids = stdout.trim().split(/\s+/);
        } catch (ex) {
          console.log(`Did not find processes in process group ${ pid }, ignoring.`);
          break;
        }

        try {
          if (pids.length > 0) {
            console.log(`Manually killing group processes ${ pids.join(' ') }`);
            await spawnFile('kill', ['-s', signal, ...pids]);
          }
        } catch (ex) {
          console.log(`Failed to process group: ${ ex } (retrying)`);
        }
        await util.promisify(setTimeout)(timeout);
      }
    }
  }
}

export async function teardown(app: ElectronApplication | undefined, testInfo: TestInfo) {
  // The app may be undefined if the app failed to launch; we still need to tear
  // down in that case, as RDD may be running.
  const context = app?.context();
  const { file: filename } = testInfo;

  if (context) {
    await context.tracing.stop({ path: reportAsset(testInfo) });
  }
  if (app) {
    await teardownApp(app);
  }

  const logsDir = reportAsset(testInfo, 'log');
  await fs.promises.mkdir(logsDir, { recursive: true });
  const rddLogs = (await fs.promises.open(path.join(logsDir, 'rdd-exec.log'), 'a')).createWriteStream();
  try {
    await spawnFile(rddExe, ['service', 'delete'], { stdio: ['ignore', rddLogs, rddLogs] });
  } catch (ex) {
    console.error(`Failed to clean up RDD: ${ ex }`);
  } finally {
    await expect(util.promisify(rddLogs.close.bind(rddLogs))(), 'failed to close RDD logs')
      .resolves.toBeUndefined();
  }
  let exit: number | null = 0;
  let signal: NodeJS.Signals | null = null;
  try {
    const { mockController } = currentTest ?? {};
    if (mockController) {
      if (mockController.exitCode === null && mockController.signalCode === null) {
        // The process has not exited yet.
        const timeout = setTimeout(() => mockController.kill('SIGKILL'), 10_000);
        const promise = new Promise<void>(resolve => mockController.once('exit', resolve));
        mockController.kill();
        await promise;
        clearTimeout(timeout);
      }
      ({ exitCode: exit, signalCode: signal } = mockController);
    }
  } catch (ex) {
    // Ignore the error if the process is already gone, but log it otherwise.
    if (!ex || typeof ex !== 'object' || !('code' in ex) || ex.code !== 'ESRCH') {
      console.error(`Failed to clean up mock controller: ${ ex }`);
    }
  }
  expect({ exit, signal }, 'mock controller exited').toEqual({ exit: 0, signal: null });

  if (currentTest?.file === filename) {
    const delta = (Date.now() - currentTest.startTime) / 1_000;
    const min = Math.floor(delta / 60);
    const sec = Math.round(delta % 60);
    const string = min ? `${ min } min ${ sec } sec` : `${ sec } seconds`;

    console.log(`Test ${ path.basename(filename) } took ${ string }.`);
  } else {
    console.log(`Test ${ path.basename(filename) } did not have a start time.`);
  }
}

export function getResourceBinDir(): string {
  const srcDir = path.dirname(import.meta.dirname);

  return path.join(srcDir, '..', 'resources', os.platform(), 'bin');
}

function exeName(executable: string) {
  return process.platform.startsWith('win') ? `${ executable }.exe` : executable;
}

export function getFullPathForTool(tool: string): string {
  if (path.isAbsolute(tool)) {
    return tool;
  }
  return path.join(getResourceBinDir(), exeName(tool));
}

/**
 * Run the given tool with the given arguments, returning its standard output.
 */
export async function tool(tool: string, ...args: string[]): Promise<string> {
  const exe = getFullPathForTool(tool);

  try {
    const { stdout } = await spawnFile(exe, args, {
      env: {
        ...process.env,
        PATH: `${ process.env.PATH }${ path.delimiter }${ getResourceBinDir() }`,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    return stdout;
  } catch (ex:any) {
    console.error(`Error running ${ tool } ${ args.join(' ') }`);
    console.error(`stdout: ${ ex.stdout }`);
    console.error(`stderr: ${ ex.stderr }`);
    // This expect(...).toBeUndefined() will always fail; we just want to make
    // playwright print out the stdout and stderr along with the message.
    // Normally, it would just print out `ex.toString()`, which mostly just says
    // "<command> exited with code 1" and doesn't explain _why_ that happened.
    expect({
      stdout: ex.stdout, stderr: ex.stderr, message: ex.toString(),
    }).toBeUndefined();
    throw ex;
  }
}

export const rddExe = path.join(import.meta.dirname, '..', '..', 'rdd', 'bin', exeName('rdd'));

/**
 * Run `rdd` with given arguments.
 * @returns standard output of the command.
 */
export async function rdd(...args: string[]): Promise<string> {
  return await tool(rddExe, ...args);
}

/**
 * Run `kubectl` with given arguments.
 * @returns standard output of the command.
 * @example await kubectl('version')
 */
export async function kubectl(...args: string[] ): Promise<string> {
  return await tool('kubectl', '--context', 'rancher-desktop', ...args);
}

/**
 * Run `helm` with given arguments.
 * @returns standard output of the command.
 * @example await helm('version')
 */
export async function helm(...args: string[] ): Promise<string> {
  return await tool('helm', '--kube-context', 'rancher-desktop', ...args);
}

export async function retry<T>(proc: () => Promise<T>, options?: { delay?: number, tries?: number }): Promise<T> {
  const delay = options?.delay ?? 500;
  const tries = options?.tries ?? 30;

  for (let i = 1; ; ++i) {
    try {
      return await proc();
    } catch (ex) {
      if (i >= tries) {
        console.log(`${ tries } tries exceeding, failing.`);
        throw ex;
      }
      console.error(`${ ex }, retrying... (${ i }/${ tries })`);
      await util.promisify(setTimeout)(delay);
    }
  }
}

export interface startRancherDesktopOptions {
  /** The environment to use. */
  env?:        Record<string, string>;
  /** Maximum time in milliseconds to wait for the app to launch. */
  timeout?:    number;
  /** A suffix to be added to the log file, for variants. */
  logVariant?: string;
}

/**
 * Run Rancher Desktop using the mock controller.
 * @param testInfo The Playwright test info object.
 * @param options Additional options; see type definition for details.
 * @returns The Electron application.
 */
export async function startRancherDesktop(testInfo: TestInfo, options: startRancherDesktopOptions = {}): Promise<ElectronApplication> {
  // If we have a previous mock controller, kill it.  This should have happened
  // in teardown(); this only exists as insurance.
  try {
    if (typeof currentTest?.mockController?.exitCode !== 'number') {
      currentTest?.mockController?.kill();
    }
  } catch { /* Ignore errors killing stale controllers */ }

  currentTest = {
    file: testInfo.file, options, startTime: Date.now(),
  };
  const topSrcDir = path.join(import.meta.dirname, '../..');
  const logsDir = reportAsset(testInfo, 'log');
  await fs.promises.rm(logsDir, {
    recursive: true, force: true, maxRetries: 3,
  });
  await fs.promises.mkdir(logsDir, { recursive: true });
  // Set the RDD_INSTANCE process-wide so we can use the correct instance for
  // running any child processes.
  process.env.RDD_INSTANCE = reportAsset(testInfo, 'instance');
  const env = {
    ...process.env,
    ...options?.env ?? {},
    RDD_LOG_DIR: logsDir,
  };

  // We need to manually launch RDD, to force the use of the mock backend.
  const rddLogs = (await fs.promises.open(path.join(logsDir, 'rdd-exec.log'), 'a')).createWriteStream();
  try {
    await spawnFile(
      path.join(topSrcDir, 'rdd', 'bin', exeName('rdd')),
      ['service', 'delete'],
      { env, stdio: ['ignore', rddLogs, rddLogs] });
    await spawnFile(
      path.join(topSrcDir, 'rdd', 'bin', exeName('rdd')),
      ['service', 'start', '--controllers=', '--wait=false'],
      { env, stdio: ['ignore', rddLogs, rddLogs] });
  } finally {
    await expect(util.promisify(rddLogs.close.bind(rddLogs))(), 'failed to close RDD logs')
      .resolves.toBeUndefined();
  }

  // The mock controller does not daemonize; launch it in the background.
  const controller = childProcess.spawn(
    path.join(topSrcDir, 'rdd', 'bin', exeName('mock-controller')),
    ['-v', '2', '--logtostderr=false', `-log_file=${ path.join(logsDir, 'mock-controller.log') }`],
    { env, stdio: 'ignore' });
  // Stash controller PID so we can kill it in teardown().
  currentTest.mockController = controller;
  controller.unref(); // Don't block test if we fail to clean up.

  const args = [
    path.join(topSrcDir, packageMeta.main),
    '--disable-gpu',
    '--whitelisted-ips=',
    // See pkg/rancher-desktop/utils/commandLine.ts before changing the next item as the final option.
    '--disable-dev-shm-usage',
  ];

  const launchOptions: Parameters<typeof _electron.launch>[0] = { args, env };

  if (options?.timeout) {
    launchOptions.timeout = options?.timeout;
  }
  const electronApp = await _electron.launch(launchOptions);

  await electronApp.context().tracing.start({ screenshots: true, snapshots: true });

  return electronApp;
}

// Start Rancher Desktop, and wait for the window to appear.
export async function startSlowerDesktop(testInfo: TestInfo): Promise<[ElectronApplication, Page]> {
  const launchOptions: startRancherDesktopOptions = { };

  if (process.env.CI) {
    launchOptions.timeout = 120_000; // default is 30_000 msec but the CI is very slow
  }
  const electronApp = await startRancherDesktop(testInfo, launchOptions);
  const page = await electronApp.firstWindow();

  return [electronApp, page];
}
