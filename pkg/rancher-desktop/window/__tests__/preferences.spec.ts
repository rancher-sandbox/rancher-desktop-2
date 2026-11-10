import { EventEmitter } from 'node:events';

import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

const showMessageBoxSync = jest.fn<(...args: any[]) => number>();
const mainEvents = new EventEmitter();
let nextId = 1;
let browserWindow: any;

mockModules({
  electron:               { app: { isPackaged: false }, dialog: { showMessageBoxSync } },
  '@pkg/window':          { webRoot: '', createWindow: () => browserWindow, getWindow: () => undefined },
  '@pkg/main/i18n':       { onLocaleChange: () => () => {}, t: (key: string) => key },
  '@pkg/main/mainEvents': mainEvents,
  '@pkg/utils/paths':     { resources: '/' },
  '@pkg/config/help':     { Help: { preferences: { openUrl: jest.fn() } } },
  '@pkg/utils/logging':   undefined,
});

const { openPreferences } = await import('@pkg/window/preferences');

/** Answer each dialog with the button that has this label, whatever its position. */
function answer(label: string) {
  showMessageBoxSync.mockImplementation((_window, options) => {
    expect(options.buttons).toContain(label);

    return options.buttons.indexOf(label);
  });
}

/**
 * Close a window whose page has unsaved changes, in Electron's order. Emit `close`,
 * then `will-prevent-unload` for the unload that the page cancels. Return whether
 * the handler let the window close. It does that by calling preventDefault().
 */
function closeWithUnsavedChanges(): boolean {
  let allowed = false;

  browserWindow.emit('close');
  browserWindow.webContents.emit('will-prevent-unload', { preventDefault: () => { allowed = true } });

  return allowed;
}

describe('preferences window close guard', () => {
  beforeEach(() => {
    browserWindow = Object.assign(new EventEmitter(), {
      id: nextId++, webContents: new EventEmitter(), show: jest.fn(), setTitle: jest.fn(),
    });
    showMessageBoxSync.mockReset();
    openPreferences();
  });

  it('keeps the window open on Cancel', () => {
    answer('generic.cancel');
    expect(closeWithUnsavedChanges()).toBe(false);
  });

  it('closes on Discard', () => {
    answer('preferencesWindow.close.discardChanges');
    expect(closeWithUnsavedChanges()).toBe(true);
  });

  it('registers its handlers once when reopened after a cancelled close', () => {
    answer('generic.cancel');
    closeWithUnsavedChanges();
    openPreferences();
    expect(browserWindow.webContents.listenerCount('before-input-event')).toBe(1);
    expect(browserWindow.webContents.listenerCount('will-prevent-unload')).toBe(1);
  });

  // Keep this test last. Its `before-quit` sets `quitting` for the rest of the file,
  // so a later test would close the window without showing the dialog.
  it('closes without asking once the app quits', () => {
    mainEvents.emit('before-quit');
    expect(closeWithUnsavedChanges()).toBe(true);
    expect(showMessageBoxSync).not.toHaveBeenCalled();
  });
});
