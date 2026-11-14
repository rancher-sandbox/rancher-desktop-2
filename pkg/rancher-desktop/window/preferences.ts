import path from 'path';

import { app } from 'electron';

import { webRoot, createWindow, getWindow } from '.';

import { Help } from '@pkg/config/help';
import { onLocaleChange, t } from '@pkg/main/i18n';
import mainEvents from '@pkg/main/mainEvents';
import paths from '@pkg/utils/paths';
import { CommandOrControl, Shortcuts } from '@pkg/utils/shortcuts';
import { preferencesNavItems } from '@pkg/window/preferenceConstants';

// Destroy Preferences without unloading its page when the app quits.  With
// unsaved changes, the page would cancel its unload to ask about them, and
// that cancels the quit too.  The app has already stopped steve by then, so
// it would keep running without steve.
mainEvents.on('before-quit', () => {
  getWindow('preferences')?.destroy();
});

/**
 * Open the Preferences window; if it is already open, focus it.
 */
export function openPreferences() {
  const window = createWindow('preferences', `${ webRoot }/index.html#preferences`, {
    title:           t('preferencesWindow.title'),
    width:           768,
    height:          512,
    autoHideMenuBar: true,
    resizable:       false,
    minimizable:     false,
    show:            false,
    icon:            path.join(paths.resources, 'icons', 'logo-square-512.png'),
    // Closing the main window destroys this child window without unloading
    // its page, so unsaved changes are dropped without asking.
    parent:          getWindow('main') ?? undefined,
    webPreferences:  {
      devTools:         !app.isPackaged,
      nodeIntegration:  true,
      contextIsolation: false,
    },
  });

  if (!Shortcuts.isRegistered(window)) {
    Shortcuts.register(
      window,
      [{
        key:      '?',
        meta:     true,
        platform: 'darwin',
      }, {
        key:      'F1',
        platform: ['win32', 'linux'],
      }],
      () => {
        Help.preferences.openUrl();
      },
      'preferences help',
    );

    Shortcuts.register(
      window,
      { key: 'Escape' },
      () => {
        window.close();
      },
      'Close preferences dialog',
    );

    preferencesNavItems.forEach(({ name }, index) => {
      Shortcuts.register(
        window,
        {
          ...CommandOrControl,
          key: index + 1,
        },
        () => window.webContents.send('route', { name }),
        `switch preferences tabs ${ name }`,
      );
    });

    Shortcuts.register(
      window,
      {
        ...CommandOrControl,
        key: ']',
      },
      () => window.webContents.send('route', { direction: 'forward' }),
      'switch preferences tabs by cycle [forward]',
    );

    Shortcuts.register(
      window,
      {
        ...CommandOrControl,
        key: '[',
      },
      () => window.webContents.send('route', { direction: 'back' }),
      'switch preferences tabs by cycle [back]',
    );

    const offLocaleChange = onLocaleChange(() => {
      window.setTitle(t('preferencesWindow.title'));
    });

    window.on('closed', offLocaleChange);
  }

  window.webContents.on('ipc-message', (_event, channel) => {
    if (channel === 'preferences/load') {
      window.show();
    }
  });

  app.dock?.show();
}
