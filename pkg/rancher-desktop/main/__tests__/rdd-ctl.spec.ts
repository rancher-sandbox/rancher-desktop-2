// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { expect, jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

const modules = mockModules({
  '@pkg/main/ipcMain': {
    getIpcMainProxy: jest.fn(() => ({ handle: jest.fn() })),
  },
  '@pkg/utils/childProcess': {
    spawnFile:  jest.fn((exe: string, args: string[]) => Promise.resolve({ stdout: '' })),
    SpawnError: jest.fn(),
  },
  '@pkg/utils/logging': undefined,
  '@pkg/utils/paths':   {
    getRDDPath: jest.fn(() => '/dev/null'),
  },
  '@pkg/window/splash': {
    hideSplash: jest.fn(),
  },
  electron: {
    app:    { quit: jest.fn() },
    dialog: { showErrorBox: jest.fn() },
  },
});

const { isFirstRunNeeded } = await import('@pkg/main/rdd-ctl');
const { default: mainEvents } = await import('@pkg/main/mainEvents');

describe('isFirstRunNeeded', () => {
  const { spawnFile } = modules['@pkg/utils/childProcess'];
  let spawnFileOutputs: Record<string, string> = {};
  beforeEach(() => {
    spawnFile.mockClear();
    spawnFileOutputs = {};
    spawnFile.mockImplementation((_exe, args) => {
      for (const [key, value] of Object.entries(spawnFileOutputs)) {
        if (args.includes(key)) {
          return Promise.resolve({ stdout: value });
        }
      }
      return Promise.resolve({ stdout: '' });
    });
  });
  afterEach(() => {
    // Check that no unexpected calls to spawnFile were made.  The order does
    // not matter, and it is perfectly fine to have made no calls.
    for (const call of spawnFile.mock.calls) {
      expect(call[1]).toEqual(expect.arrayContaining([expect.toBeOneOf(...Object.keys(spawnFileOutputs))]));
    }
  });

  it('should return true if control plane is not created', async() => {
    spawnFileOutputs = {
      status: JSON.stringify({ created: false }),
    };
    await expect(isFirstRunNeeded()).resolves.toBe(true);
  });

  it('should return true if control plane exists but app does not', async() => {
    spawnFileOutputs = {
      status: JSON.stringify({ created: true }),
      app:    '  \t  \r\n',
    };
    await expect(isFirstRunNeeded()).resolves.toBe(true);
  });

  it('should return false if control plane and app both exist', async() => {
    spawnFileOutputs = {
      status: JSON.stringify({ created: true }),
      app:    'app.app.rancherdesktop.io/app\n',
    };
    await expect(isFirstRunNeeded()).resolves.toBe(false);
  });

  it('should return false if an error occurs', async() => {
    spawnFileOutputs = {
      status: 'ignored',
    };
    spawnFile.mockRejectedValue(new Error('Some error'));
    await expect(isFirstRunNeeded()).resolves.toBe(false);
  });
});

describe('rdd/kube-config', () => {
  it('should hide the splash before reporting an incompatible service', async() => {
    const { spawnFile, SpawnError } = modules['@pkg/utils/childProcess'];
    const { hideSplash } = modules['@pkg/window/splash'];
    const { showErrorBox } = modules.electron.dialog;
    const error = new (SpawnError as any)();

    error.code = 5;
    spawnFile.mockRejectedValue(error);
    await expect(mainEvents.invoke('rdd/kube-config')).rejects.toThrow('Unsupported RDD service version');
    expect(showErrorBox).toHaveBeenCalled();
    expect(hideSplash).toHaveBeenCalled();
    expect(hideSplash.mock.invocationCallOrder[0]).toBeLessThan(showErrorBox.mock.invocationCallOrder[0]);
  });
});
