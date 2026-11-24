import { EventEmitter } from 'node:events';
import os from 'node:os';

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
const hideSplash = jest.fn();
const markStartup = jest.fn();
const showErrorBox = jest.fn();
const quit = jest.fn();

mockModules({
  electron: {
    app:             { quit },
    ipcMain:         {},
    BrowserWindow:   Object.assign(FakeWindow, { fromId: () => null }),
    shell:           {},
    nativeTheme:     {},
    screen:          {},
    WebContentsView: class {},
    dialog:          { showErrorBox },
  },
  '@pkg/utils/logging':       { default: new Proxy({}, { get: () => ({ debug: jest.fn(), error: jest.fn() }) }) },
  '@pkg/window/splash':       { closeSplash, hideSplash, themeBackgroundColor: () => '#FFFFFF' },
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
    hideSplash.mockClear();
    markStartup.mockClear();
    showErrorBox.mockClear();
    quit.mockClear();
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

  it.each([
    { kind: 'visible', options: {} },
    { kind: 'hidden', options: { show: false } },
  ])('shows an error box and quits when a $kind first window fails to load', async({ options }) => {
    const window = await createFirstWindow(options);
    const { default: paths } = await import('@pkg/utils/paths');

    window.webContents.emit('did-fail-load', {}, -9, 'ERR_UNEXPECTED', 'app://./index.html', true);
    expect(hideSplash).toHaveBeenCalled();
    expect(showErrorBox).toHaveBeenCalledTimes(1);
    expect(showErrorBox).toHaveBeenCalledWith(expect.stringContaining('Fatal Error'), expect.stringContaining('(ERR_UNEXPECTED)'));
    const shownLogDir = process.platform === 'win32' ? paths.log_dir : `~${ paths.log_dir.slice(os.homedir().length) }`;

    expect((showErrorBox.mock.calls[0][1] as string).split('\n').pop()).toBe(shownLogDir);
    expect(quit).toHaveBeenCalled();
    expect(hideSplash.mock.invocationCallOrder[0]).toBeLessThan(showErrorBox.mock.invocationCallOrder[0]);
    expect(showErrorBox.mock.invocationCallOrder[0]).toBeLessThan(quit.mock.invocationCallOrder[0]);
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
    expect(showErrorBox).not.toHaveBeenCalled();
    expect(quit).not.toHaveBeenCalled();
  });

  it('ignores an aborted first page load', async() => {
    const window = await createFirstWindow({});

    window.webContents.emit('did-fail-load', {}, -3, '', 'app://./index.html', true);
    expect(showErrorBox).not.toHaveBeenCalled();
    expect(quit).not.toHaveBeenCalled();
  });

  it('ignores a failed load after the first window has loaded', async() => {
    const window = await createFirstWindow({});

    window.webContents.emit('did-finish-load');
    window.webContents.emit('did-fail-load', {}, -105, 'ERR_NAME_NOT_RESOLVED', 'app://./index.html', true);
    expect(showErrorBox).not.toHaveBeenCalled();
    expect(quit).not.toHaveBeenCalled();
  });
});
