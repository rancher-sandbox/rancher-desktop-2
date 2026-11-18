import { EventEmitter } from 'node:events';

import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

const mainEvents = new EventEmitter();
let nextId = 1;
let browserWindow: any;
const mainWindow = { id: 0 };

mockModules({
  electron:               { app: { isPackaged: false } },
  '@pkg/window':          { webRoot: '', createWindow: () => browserWindow, getWindow: (name: string) => name === 'preferences' ? browserWindow : undefined },
  '@pkg/main/i18n':       { onLocaleChange: () => () => {}, t: (key: string) => key },
  '@pkg/main/mainEvents': mainEvents,
  '@pkg/utils/paths':     { resources: '/' },
  '@pkg/config/help':     { Help: { preferences: { openUrl: jest.fn() } } },
  '@pkg/utils/logging':   undefined,
});

const { openPreferences } = await import('@pkg/window/preferences');

describe('preferences window', () => {
  beforeEach(() => {
    browserWindow = Object.assign(new EventEmitter(), {
      id:              nextId++,
      webContents:     new EventEmitter(),
      show:            jest.fn(),
      setTitle:        jest.fn(),
      destroy:         jest.fn(),
      close:           jest.fn(),
      isDestroyed:     () => false,
      getParentWindow: () => mainWindow,
      setParentWindow: jest.fn(),
    });
    openPreferences();
  });

  it('reattaches to the main window after Escape asks it to close', () => {
    browserWindow.webContents.emit('before-input-event', { preventDefault: jest.fn() }, { key: 'Escape' });
    expect(browserWindow.close).toHaveBeenCalled();
    expect(browserWindow.setParentWindow).toHaveBeenCalledWith(mainWindow);
    expect(browserWindow.setParentWindow.mock.invocationCallOrder[0]).toBeGreaterThan(browserWindow.close.mock.invocationCallOrder[0]);
  });

  it('registers its shortcuts once when reopened after a cancelled close', () => {
    // The page cancelled its unload, so the window emits `close` but not `closed`.
    browserWindow.emit('close');
    openPreferences();
    expect(browserWindow.webContents.listenerCount('before-input-event')).toBe(1);
  });

  it('is destroyed when the app quits', () => {
    mainEvents.emit('before-quit');
    expect(browserWindow.destroy).toHaveBeenCalled();
  });
});
