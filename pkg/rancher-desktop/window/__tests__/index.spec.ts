import { EventEmitter } from 'node:events';

import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

class FakeWindow extends EventEmitter {
  static last: FakeWindow;
  id = 1;
  loadURL = jest.fn();
  webContents = Object.assign(new EventEmitter(), { setWindowOpenHandler: jest.fn() });

  constructor() {
    super();
    FakeWindow.last = this;
  }
}

const closeSplash = jest.fn();
const markStartup = jest.fn();

mockModules({
  electron: {
    app:             {},
    ipcMain:         {},
    BrowserWindow:   Object.assign(FakeWindow, { fromId: () => null }),
    shell:           {},
    nativeTheme:     {},
    screen:          {},
    WebContentsView: class {},
  },
  '@pkg/utils/logging':       { default: new Proxy({}, { get: () => ({ debug: jest.fn(), error: jest.fn() }) }) },
  '@pkg/window/splash':       { closeSplash, themeBackgroundColor: () => '#FFFFFF' },
  '@pkg/utils/startupTiming': { markStartup, reportStartup: jest.fn() },
});

/** Create the first window in a fresh copy of the module. */
async function createFirstWindow(options: { show?: boolean }) {
  jest.resetModules();
  const { createWindow } = await import('@pkg/window');

  createWindow('main', 'app://./index.html', options);

  return FakeWindow.last;
}

describe('createWindow', () => {
  beforeEach(() => {
    closeSplash.mockClear();
    markStartup.mockClear();
  });

  it('closes the splash when a hidden first window shows', async() => {
    const window = await createFirstWindow({ show: false });

    window.webContents.emit('did-finish-load');
    expect(closeSplash).not.toHaveBeenCalled();
    window.emit('show');
    expect(closeSplash).toHaveBeenCalled();
    expect(markStartup).toHaveBeenLastCalledWith('first-window-shown');
  });

  it('closes the splash when a visible first window loads', async() => {
    const window = await createFirstWindow({});

    window.webContents.emit('did-finish-load');
    expect(closeSplash).toHaveBeenCalled();
    expect(markStartup).toHaveBeenLastCalledWith('first-window-loaded');
  });

  it('closes the splash when the first window closes before loading', async() => {
    const window = await createFirstWindow({});

    window.emit('closed');
    expect(closeSplash).toHaveBeenCalled();
    expect(markStartup).toHaveBeenLastCalledWith('first-window-closed');
  });

  it('closes the splash when a visible first window fails to load', async() => {
    const window = await createFirstWindow({});

    window.webContents.emit('did-fail-load', {}, -105, 'ERR_NAME_NOT_RESOLVED', 'app://./index.html', true);
    expect(closeSplash).toHaveBeenCalled();
    expect(markStartup).toHaveBeenLastCalledWith('first-window-failed');
  });

  it('closes the splash when a visible first window crashes while loading', async() => {
    const window = await createFirstWindow({});

    window.webContents.emit('render-process-gone', {}, { reason: 'crashed', exitCode: 1 });
    expect(closeSplash).toHaveBeenCalled();
    expect(markStartup).toHaveBeenLastCalledWith('first-window-crashed');
  });

  it('keeps the splash when a sub-frame fails to load', async() => {
    const window = await createFirstWindow({});

    window.webContents.emit('did-fail-load', {}, -105, 'ERR_NAME_NOT_RESOLVED', 'app://./frame.html', false);
    expect(closeSplash).not.toHaveBeenCalled();
  });

  it('keeps the splash when a hidden first window fails to load', async() => {
    const window = await createFirstWindow({ show: false });

    window.webContents.emit('did-fail-load', {}, -105, 'ERR_NAME_NOT_RESOLVED', 'app://./index.html', true);
    expect(closeSplash).not.toHaveBeenCalled();
  });
});
