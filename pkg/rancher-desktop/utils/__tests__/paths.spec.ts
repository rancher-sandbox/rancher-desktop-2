import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { jest } from '@jest/globals';
import pick from 'lodash/pick';

import mockModules from '@pkg/utils/testUtils/mockModules';

import type { Paths } from '../paths';

const modules = mockModules({
  child_process: {
    spawnSync: jest.fn(),
  },
  electron: {
    app: {
      isPackaged: false,
    },
  },
  fs: {
    ...fs,
    accessSync: jest.spyOn(fs, 'accessSync').mockReturnValue(undefined),
  },
  which: {
    sync: jest.fn(),
  },
});

const { getRDDPath, getPaths, TEST_PLATFORM } = await import('../paths');

describe('getRDDPath', () => {
  const exeName = process.platform === 'win32' ? 'rdd.exe' : 'rdd';
  let resourcesPath: string, packagedPath: string, relativePath: string, environmentPath: string;
  beforeAll(async() => {
    resourcesPath = await fs.promises.mkdtemp(path.join(os.tmpdir(), 'r-d-a-paths-'));
    packagedPath = path.join(resourcesPath, process.platform, 'bin', exeName);
    relativePath = path.join(process.cwd(), 'rdd', 'bin', exeName);
    environmentPath = path.join(resourcesPath, exeName);
  });
  afterAll(async() => {
    await fs.promises.rm(resourcesPath, { recursive: true });
  });
  describe('when packaged', () => {
    beforeEach(() => {
      // The tests run under Node.JS, so `process.resourcesPath` is not set.
      (process as any).resourcesPath ||= resourcesPath;
      jest.replaceProperty(process, 'resourcesPath', resourcesPath);
      jest.replaceProperty(modules.electron.app, 'isPackaged', true);
    });
    it('should return packaged path if it exists', () => {
      const actual = getRDDPath(false);
      expect(modules.fs.accessSync).toHaveBeenCalledWith(packagedPath, fs.constants.X_OK);
      expect(actual).toBe(packagedPath);
    });
    it('should ignore inaccessible packaged path', () => {
      modules.fs.accessSync.mockImplementationOnce(() => { throw new Error('inaccessible') });
      const actual = getRDDPath(false);
      expect(modules.fs.accessSync).toHaveBeenCalledWith(relativePath, fs.constants.X_OK);
      expect(actual).toBe(relativePath);
    });
  });
  describe('when not packaged', () => {
    beforeEach(() => {
      jest.replaceProperty(modules.electron.app, 'isPackaged', false);
      modules.fs.accessSync.mockClear();
    });
    it('should return relative path if it exists', () => {
      const actual = getRDDPath(false);
      expect(modules.fs.accessSync).toHaveBeenCalledWith(relativePath, fs.constants.X_OK);
      expect(actual).toBe(relativePath);
    });
    it('should return PATH path if it exists', () => {
      modules.fs.accessSync.mockImplementationOnce(() => { throw new Error('inaccessible') });
      modules.which.sync.mockReturnValue(environmentPath);
      expect(getRDDPath(false)).toBe(environmentPath);
    });
    it('should throw if no path is found', () => {
      modules.fs.accessSync.mockImplementation(() => { throw new Error('inaccessible') });
      modules.which.sync.mockReturnValue(null);
      expect(() => getRDDPath(false)).toThrow();
      expect(modules.which.sync).toHaveBeenCalledWith(exeName, { nothrow: true });
    });
  });
});

describe('getPaths', () => {
  const rddPaths = {
    resources:     'resources',
    cache:         'cache',
    args_file:     'args_file',
    config:        'config',
    dir:           'dir',
    docker_socket: 'docker_socket',
    lima_home:     'lima_home',
    log_dir:       'log_dir',
    pid_file:      'pid_file',
    short_dir:     'short_dir',
    tls_dir:       'tls_dir',
  };
  const expected: Paths = {
    ...rddPaths,
    rdd:              'rdd',
    resources:        path.join(process.cwd(), 'resources'),
    binDir:           path.join(process.cwd(), 'resources', process.platform, 'bin'),
    internalDir:      path.join(process.cwd(), 'resources', 'internal'),
    dockerPluginsDir: path.join(process.cwd(), 'resources', process.platform, 'docker-cli-plugins'),
    cache:            path.join(rddPaths.dir, 'cache'),
  };
  beforeEach(() => {
    // The tests run under Node.JS, so `process.resourcesPath` is not set.
    (process as any).resourcesPath ||= expected.resources;
    jest.replaceProperty(process, 'resourcesPath', expected.resources);
  });
  it('should throw on unsupported platform', () => {
    modules.child_process.spawnSync.mockReturnValue({
      stdout: JSON.stringify(rddPaths),
      status: 0,
    });
    expect(() => {
      getPaths('unused', 'AmigaOS' as any);
    }).toThrow(/Unsupported platform: AmigaOS/);
  });
  it('should return all paths', () => {
    modules.child_process.spawnSync.mockReturnValue({
      stdout: JSON.stringify(rddPaths),
      status: 0,
    });
    const actual = getPaths(expected.rdd, TEST_PLATFORM as any);
    expect(actual).toEqual(expected);
  });
  describe('should return fallback paths if RDD fails', () => {
    it.each([
      ['by throwing an error', () => { throw new Error('spawn error') }],
      ['by returning an error', () => { return { error: new Error('spawn error') } }],
      ['by returning a non-zero exit code', () => { return { status: 1, stderr: 'some error' } }],
      ['by returning invalid JSON', () => { return { stdout: 'not json', status: 0 } }],
    ])('%s', (_, mockImplementation) => {
      modules.child_process.spawnSync.mockImplementation(mockImplementation);
      const expectedFallback: Partial<Paths> = {
        dir:       path.join(os.tmpdir(), 'rancher-desktop'),
        cache:     path.join(os.tmpdir(), 'rancher-desktop', 'cache'),
        log_dir:   path.join(os.tmpdir(), 'rancher-desktop', 'log_dir'),
        ...pick(expected, 'resources', 'binDir', 'internalDir', 'dockerPluginsDir', 'rdd'),
      };
      const actual = getPaths(expected.rdd, TEST_PLATFORM as any);
      for (const untypedKey of Object.keys(expected)) {
        const key = untypedKey as keyof Paths;
        if (key in expectedFallback) {
          expect(actual[key]).toEqual(expectedFallback[key]);
        } else {
          expect(() => actual[key]).toThrow(new RegExp(`\\b${ key }\\b`));
        }
      }
      // It should not break things inherited from Object.prototype.
      expect(() => actual.toString()).not.toThrow();
    });
  });
  it('should return undefined if resources is missing in production', () => {
    jest.replaceProperty(modules.electron.app, 'isPackaged', true);
    jest.replaceProperty(process, 'resourcesPath', undefined as any);
    modules.child_process.spawnSync.mockReturnValue({
      stdout: '{}',
      status: 0,
    });
    const actual = getPaths(expected.rdd, TEST_PLATFORM as any);
    expect(actual).toHaveProperty('resources', undefined);
  });
});
