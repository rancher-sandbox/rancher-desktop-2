import { EventEmitter } from 'node:events';

import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

const port = 9443;
const onIPCMainProxy = jest.fn((event: unknown, cb: (event: unknown, locale: string) => void) => {});
const windowMapping: Record<string, number> = {};
const cookieWrites: { cookie: unknown, resolve: () => void }[] = [];
const log = { error: jest.fn() };

class FakeWindow extends EventEmitter {
  static windows: FakeWindow[] = [];
  static fromId = (id: number) => FakeWindow.windows.find(w => w.id === id) ?? null;
  id = FakeWindow.windows.length + 1;
  destroyed = false;
  loadURL = jest.fn();
  reload = jest.fn();
  setTitle = jest.fn();
  isDestroyed = () => this.destroyed;
  webContents = Object.assign(new EventEmitter(), {
    session: {
      cookies: {
        set: (cookie: unknown) => new Promise<void>((resolve) => {
          cookieWrites.push({ cookie, resolve });
        }),
      },
    },
  });

  constructor() {
    super();
    FakeWindow.windows.push(this);
  }

  close() {
    this.destroyed = true;
    this.emit('closed');
  }
}

mockModules({
  '@pkg/utils/logging': { default: new Proxy({}, { get: () => log }) },
  '@pkg/main/ipcMain':  {
    getIpcMainProxy: jest.fn(() => ({
      on:   onIPCMainProxy,
      once: jest.fn(),
      send: jest.fn(),
    })),
  },
  electron:              { BrowserWindow: FakeWindow },
  '@pkg/backend/steve':  { Steve: { getInstance: () => ({ port }) } },
  '@pkg/window':         {
    windowMapping,
    restoreWindow: (window: FakeWindow | null) => !!window,
  },
});

const { initMainI18n } = await import('@pkg/main/i18n');
const { openDashboard, closeDashboard } = await import('@pkg/window/dashboard');

initMainI18n();
const changeLocale = onIPCMainProxy.mock.calls.find(args => args[0] === 'i18n/locale-change')![1];
const settle = () => new Promise(resolve => setTimeout(resolve));

describe('openDashboard', () => {
  afterEach(() => {
    closeDashboard();
    changeLocale(null, 'en-us');
    delete windowMapping['dashboard'];
    cookieWrites.length = 0;
    FakeWindow.windows.length = 0;
  });

  it('sets the locale cookie before loading, and opens only one window', async() => {
    const opened = openDashboard();
    const second = openDashboard();

    expect(FakeWindow.windows).toHaveLength(1);
    expect(cookieWrites.map(({ cookie }) => cookie)).toEqual([
      { url: `http://127.0.0.1:${ port }/`, name: 'R_LOCALE', value: 'en-us' },
    ]);
    expect(FakeWindow.windows[0].loadURL).not.toHaveBeenCalled();
    cookieWrites[0].resolve();
    await Promise.all([opened, second]);
    expect(FakeWindow.windows[0].loadURL).toHaveBeenCalledWith(`http://127.0.0.1:${ port }/c/local/explorer`);
  });

  it('does not load a window closed during the first cookie write', async() => {
    const opened = openDashboard();

    FakeWindow.windows[0].close();
    cookieWrites[0].resolve();
    await opened;
    expect(FakeWindow.windows[0].loadURL).not.toHaveBeenCalled();
  });

  it('writes a new locale to the cookie, then reloads', async() => {
    const opened = openDashboard();

    cookieWrites[0].resolve();
    await opened;
    changeLocale(null, 'de');
    expect(cookieWrites[1].cookie).toMatchObject({ name: 'R_LOCALE', value: 'de' });
    expect(FakeWindow.windows[0].reload).not.toHaveBeenCalled();
    cookieWrites[1].resolve();
    await settle();
    expect(FakeWindow.windows[0].reload).toHaveBeenCalled();
  });

  it('does not reload a window closed during a locale change', async() => {
    const opened = openDashboard();

    cookieWrites[0].resolve();
    await opened;
    changeLocale(null, 'de');
    FakeWindow.windows[0].close();
    cookieWrites[1].resolve();
    await settle();
    expect(FakeWindow.windows[0].reload).not.toHaveBeenCalled();
  });

  it('logs a failed load', async() => {
    const opened = openDashboard();

    cookieWrites[0].resolve();
    await opened;
    FakeWindow.windows[0].webContents.emit('did-fail-load', {}, -102, 'ERR_CONNECTION_REFUSED', `http://127.0.0.1:${ port }/c/local/explorer`);
    expect(log.error).toHaveBeenCalledWith(expect.stringContaining('ERR_CONNECTION_REFUSED'));
  });
});
