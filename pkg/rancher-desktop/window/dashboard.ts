import { BrowserWindow } from 'electron';

import { windowMapping, restoreWindow } from '.';

import { Steve } from '@pkg/backend/steve';
import { getLocale, onLocaleChange, t } from '@pkg/main/i18n';
import Logging from '@pkg/utils/logging';

const console = Logging.steve;

const getDashboardWindow = () => ('dashboard' in windowMapping) ? BrowserWindow.fromId(windowMapping['dashboard']) : null;

/**
 * Select the dashboard's language with the R_LOCALE cookie, which the dashboard
 * reads when it loads.
 */
async function setDashboardLocale(window: BrowserWindow, port: number) {
  try {
    await window.webContents.session.cookies.set({
      url:   `http://127.0.0.1:${ port }/`,
      name:  'R_LOCALE',
      value: getLocale(),
    });
  } catch (err) {
    // Any earlier cookie stays, so the dashboard keeps that language.
    console.error('Failed to set the dashboard locale:', err);
  }
}

export async function openDashboard() {
  const existingWindow = getDashboardWindow();

  if (restoreWindow(existingWindow)) {
    return existingWindow;
  }

  const { port } = Steve.getInstance();

  if (!port) {
    return;
  }

  const window = new BrowserWindow({
    title:  t('dashboard.windowTitle'),
    width:  800,
    height: 600,
    show:   false,
  });

  // Record the window before the first await, so a second click finds it.
  windowMapping['dashboard'] = window.id;

  const offLocaleChange = onLocaleChange(async() => {
    window.setTitle(t('dashboard.windowTitle'));
    await setDashboardLocale(window, port);
    // The dashboard sets some strings only once, at startup, so switching it
    // in place would leave them in the old language.
    if (!window.isDestroyed()) {
      window.reload();
    }
  });

  window.on('closed', offLocaleChange);

  window.once('ready-to-show', () => {
    window.show();
  });

  await setDashboardLocale(window, port);
  if (!window.isDestroyed()) {
    window.loadURL(`http://127.0.0.1:${ port }/c/local/explorer`);
  }
}

export function closeDashboard() {
  getDashboardWindow()?.close();
}
