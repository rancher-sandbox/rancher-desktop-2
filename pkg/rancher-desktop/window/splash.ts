import path from 'path';
import { pathToFileURL } from 'url';

import { BrowserWindow, app, nativeTheme } from 'electron';

import Logging from '@pkg/utils/logging';
import paths from '@pkg/utils/paths';
import { markStartup } from '@pkg/utils/startupTiming';

const console = Logging.background;

// Body background and text colors from the app's light and dark themes
// (assets/styles/themes), so the switch to the first app window does not
// flash a different color.
const lightBackground = '#FFFFFF';
const lightText = '#141419';
const darkBackground = '#1b1c21';
const darkText = '#ffffff';

let splash: BrowserWindow | null = null;

/**
 * The body background color of the app's current light or dark theme, for a
 * window to show before its page paints.
 */
export function themeBackgroundColor(): string {
  return nativeTheme.shouldUseDarkColors ? darkBackground : lightBackground;
}

function escapeHTML(text: string): string {
  return text.replace(/[&<>"']/g, c => `&#${ c.charCodeAt(0) };`);
}

function splashHTML(name: string, version: string): string {
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src file:; style-src 'unsafe-inline'">
<style>
  html, body { margin: 0; height: 100%; overflow: hidden; cursor: default; user-select: none; }
  body {
    display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 12px;
    font-family: system-ui, sans-serif; background: ${ lightBackground }; color: ${ lightText };
  }
  @media (prefers-color-scheme: dark) {
    body { background: ${ darkBackground }; color: ${ darkText }; }
  }
  img { width: 112px; height: 112px; }
  .name { font-size: 18px; font-weight: 600; }
  .version { font-size: 13px; opacity: 0.7; }
  .spinner {
    width: 24px; height: 24px; margin-top: 8px; border-radius: 50%;
    border: 3px solid currentColor; border-top-color: transparent; opacity: 0.6;
    animation: spin 1s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
</head>
<body>
<img src="icons/logo-square-512.png" alt="">
<div class="name">${ escapeHTML(name) }</div>
<div class="version">${ escapeHTML(version) }</div>
<div class="spinner"></div>
</body>
</html>`;
}

/**
 * Show a small window with the logo, product name and version, to cover the
 * time between Electron becoming ready and the first app window appearing.
 */
export function openSplash() {
  // e2e tests (including the screenshot specs) call electronApp.firstWindow(),
  // which would return the splash instead of the first app window.
  if ((process.env.RD_TEST ?? '').includes('e2e') || splash) {
    return;
  }
  try {
    splash = new BrowserWindow({
      width:           340,
      height:          280,
      frame:           false,
      resizable:       false,
      minimizable:     false,
      maximizable:     false,
      fullscreenable:  false,
      center:          true,
      show:            false,
      backgroundColor: themeBackgroundColor(),
      webPreferences:  {
        sandbox:          true,
        contextIsolation: true,
        nodeIntegration:  false,
      },
    });
  } catch (ex) {
    console.error('Failed to create the splash window:', ex);

    return;
  }
  splash.once('ready-to-show', showSplash);
  splash.on('closed', () => {
    splash = null;
  });
  // Electron attaches a no-op rejection handler to the promise that loadURL()
  // returns, so a failed load goes unreported unless this handler logs it.
  splash.webContents.on('did-fail-load', (_event, errorCode, errorDescription) => {
    console.error(`Failed to load the splash window: ${ errorCode } (${ errorDescription })`);
    hideSplash();
  });

  const html = splashHTML(app.name, process.env.RD_VERSION ?? '');
  const baseURL = pathToFileURL(paths.resources + path.sep).href;

  splash.loadURL(`data:text/html;charset=utf-8,${ encodeURIComponent(html) }`, { baseURLForDataURL: baseURL });
}

function showSplash() {
  if (splash && !splash.isDestroyed()) {
    splash.show();
    markStartup('splash-shown');
  }
}

/**
 * Hide the splash window, if it is open, without closing it.  While the splash
 * is the app's only window, closing it would run the window-all-closed handler,
 * which hides the dock icon on macOS.  That also deactivates the app, so a
 * native error box shown next opens while another app stays frontmost.
 */
export function hideSplash() {
  if (splash && !splash.isDestroyed()) {
    splash.off('ready-to-show', showSplash);
    splash.hide();
  }
}

/**
 * Remove the splash window, if it is open.
 */
export function closeSplash() {
  if (splash && !splash.isDestroyed()) {
    splash.destroy();
  }
  splash = null;
}
