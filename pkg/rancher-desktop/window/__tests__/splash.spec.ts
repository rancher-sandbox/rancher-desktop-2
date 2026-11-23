import { EventEmitter } from 'node:events';

import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

const log = { error: jest.fn() };

class FakeWindow extends EventEmitter {
  static last:             FakeWindow;
  static constructorError: Error | undefined;
  show = jest.fn();
  hide = jest.fn();
  destroy = jest.fn();
  isDestroyed = () => false;
  loadURL = jest.fn(() => Promise.resolve());
  webContents = new EventEmitter();

  constructor() {
    super();
    if (FakeWindow.constructorError) {
      throw FakeWindow.constructorError;
    }
    FakeWindow.last = this;
  }
}

mockModules({
  '@pkg/utils/logging': { default: new Proxy({}, { get: () => log }) },
  electron:             {
    BrowserWindow: FakeWindow,
    app:           { name: 'Rancher Desktop' },
    nativeTheme:   { shouldUseDarkColors: false },
  },
});

const { openSplash, hideSplash, closeSplash } = await import('@pkg/window/splash');

describe('openSplash', () => {
  afterEach(() => {
    closeSplash();
    FakeWindow.constructorError = undefined;
    log.error.mockClear();
  });

  it('shows the splash once its page is ready', () => {
    openSplash();
    FakeWindow.last.emit('ready-to-show');
    expect(FakeWindow.last.show).toHaveBeenCalled();
  });

  it('keeps the splash hidden when its page becomes ready after hideSplash()', () => {
    openSplash();
    hideSplash();
    FakeWindow.last.emit('ready-to-show');
    expect(FakeWindow.last.show).not.toHaveBeenCalled();
  });

  it('logs a failed load and hides the splash without closing it', () => {
    openSplash();
    FakeWindow.last.webContents.emit('did-fail-load', {}, -2, 'ERR_FAILED', 'data:text/html', true);
    expect(log.error).toHaveBeenCalledWith(expect.stringContaining('-2 (ERR_FAILED)'));
    expect(FakeWindow.last.hide).toHaveBeenCalled();
    expect(FakeWindow.last.destroy).not.toHaveBeenCalled();
  });

  it('logs the error and does not throw when the window cannot be created', () => {
    FakeWindow.constructorError = new Error('no window');
    expect(() => openSplash()).not.toThrow();
    expect(log.error).toHaveBeenCalledWith(expect.any(String), FakeWindow.constructorError);
  });
});
