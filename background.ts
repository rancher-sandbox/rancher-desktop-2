import fs from 'fs';
import os from 'os';
import path from 'path';

import Electron from 'electron';
import _ from 'lodash';
import semver from 'semver';

import { Steve } from '@pkg/backend/steve';
import { Help } from '@pkg/config/help';
import { getIpcMainProxy } from '@pkg/main/ipcMain';
import mainEvents from '@pkg/main/mainEvents';
import buildApplicationMenu from '@pkg/main/mainmenu';
import setupNetworking from '@pkg/main/networking';
import initializeTransientPreferences from '@pkg/main/transientPreferences';
import Logging, { clearLoggingDirectory } from '@pkg/utils/logging';
import { fetchMacOsVersion, getMacOsVersion } from '@pkg/utils/osVersion';
import paths from '@pkg/utils/paths';
import { protocolsRegistered, setupProtocolHandlers } from '@pkg/utils/protocols';
import getWSLVersion from '@pkg/utils/wslVersion';
import * as window from '@pkg/window';
import '@pkg/main/rdd-ctl';
import { closeDashboard, openDashboard } from '@pkg/window/dashboard';
import { openPreferences } from '@pkg/window/preferences';

// https://www.electronjs.org/docs/latest/breaking-changes#changed-gtk-4-is-default-when-running-gnome
if (process.platform === 'linux') {
  Electron.app.commandLine.appendSwitch('gtk-version', '3');
}

Electron.app.setPath('userData', path.join(paths.dir, 'electron'));
Electron.app.setPath('cache', paths.cache);
Electron.app.setAppLogsPath(paths.log_dir);

const console = Logging.background;

if (!Electron.app.requestSingleInstanceLock()) {
  process.exit(201);
}

clearLoggingDirectory();

const ipcMainProxy = getIpcMainProxy(console);

let gone = false; // when true indicates app is shutting down
const noModalDialogs = false;

// Scheme must be registered before the app is ready
Electron.protocol.registerSchemesAsPrivileged([
  { scheme: 'app', privileges: { secure: true, standard: true } },
]);

process.on('unhandledRejection', (reason) => {
  console.error('UnhandledRejectionWarning:', reason);
});

Electron.app.on('second-instance', async() => {
  await protocolsRegistered;
  console.warn('A second instance was started');
  window.openMain();
});

Electron.protocol.registerSchemesAsPrivileged([{ scheme: 'app' }, {
  scheme:     'x-rd-extension',
  privileges: {
    standard:            true,
    secure:              true,
    bypassCSP:           true,
    allowServiceWorkers: true,
    supportFetchAPI:     true,
    corsEnabled:         true,
  },
}]);

Electron.app.whenReady().then(async() => {
  try {
    initializeTransientPreferences();

    setupProtocolHandlers();

    // make sure we have the macOS version cached before calling getMacOsVersion()
    if (os.platform() === 'darwin') {
      await fetchMacOsVersion(console);
    }

    // Needs to happen before any file is written; otherwise, that file
    // could be owned by root, which will lead to future problems.
    if (['linux', 'darwin'].includes(os.platform())) {
      await checkForRootPrivs();
    }
    // Check for required OS versions and features
    await checkPrerequisites();

    await setupNetworking();

    initUI();
  } catch (ex: any) {
    console.error(`Error starting up: ${ ex }`, ex.stack);
    gone = true;
    Electron.app.quit();
  }
});

function initUI() {
  if (gone) {
    console.log('User triggered quit during first-run');

    return;
  }

  buildApplicationMenu();

  Electron.app.setAboutPanelOptions({
    // TODO: Update this to 2021-... as dev progresses
    // also needs to be updated in electron-builder.yml
    copyright:          'Copyright © 2021-2026 SUSE LLC',
    applicationName:    `${ Electron.app.name } by SUSE`,
    applicationVersion: `Version ${ process.env.RD_VERSION }`,
    iconPath:           path.join(paths.resources, 'icons', 'logo-square-512.png'),
  });
  // TODO: Tray.getInstance().show();

  window.openMain();
}

async function checkForRootPrivs() {
  if (isRoot()) {
    await window.openDenyRootDialog();
    gone = true;
    Electron.app.quit();
  }
}

async function checkPrerequisites() {
  const osPlatform = os.platform();
  let messageId: window.reqMessageId = 'ok';
  let args: any[] = [];

  switch (osPlatform) {
  case 'win32': {
    // Required: Windows 10-1909(build 18363) or newer
    const winRel = os.release().split('.');

    if (Number(winRel[0]) < 10 || (Number(winRel[0]) === 10 && Number(winRel[2]) < 18363)) {
      messageId = 'win32-release';
    } else {
      try {
        const version = await getWSLVersion();

        if (version.outdated_kernel) {
          messageId = 'win32-kernel';
          args = [version];
        }
      } catch (ex) {
        console.error(`Failed to check WSL version, ignoring:`, ex);
      }
    }
    break;
  }
  case 'linux': {
    // TODO: This whole testing for nested virtualization is wrong. All we should test for is if
    // hardware acceleration is available, e.g. checking /proc/cpuinfo for "vmx" (Intel) or "svm" (AMD).
    if (process.arch === 'x64') {
      // Required: Nested virtualization enabled
      const nestedFiles = [
        '/sys/module/kvm_amd/parameters/nested',
        '/sys/module/kvm_intel/parameters/nested'];

      messageId = 'linux-nested';
      for (const nestedFile of nestedFiles) {
        try {
          const data = await fs.promises.readFile(nestedFile, { encoding: 'utf8' });

          if (data && (data.toLowerCase().startsWith('y') || data.startsWith('1'))) {
            messageId = 'ok';
            break;
          }
        } catch {
        }
      }
    }
    break;
  }
  case 'darwin': {
    // Required: macOS-10.15(Darwin-19) or newer
    if (semver.gt('10.15.0', getMacOsVersion())) {
      messageId = 'macOS-release';
    }
    break;
  }
  }

  if (messageId !== 'ok') {
    await window.openUnmetPrerequisitesDialog(messageId, ...args);
    gone = true;
    Electron.app.quit();
  }
}

Electron.app.on('before-quit', (event) => {
  if (gone) {
    mainEvents.emit('quit');

    return;
  }
  event.preventDefault();

  try {
    mainEvents.emit('before-quit');
    console.log(`2: Child exited cleanly.`);
  } catch (ex: any) {
    console.log(`2: Child exited with code ${ ex.errCode ?? '<unknown>' }`);
    handleFailure(ex);
  } finally {
    gone = true;
    setImmediate(() => {
      Electron.app.quit();
    });
  }
});

Electron.app.on('window-all-closed', () => {
  // On macOS, hide the dock icon.
  Electron.app.dock?.hide();
});

Electron.app.on('activate', async() => {
  // On macOS it's common to re-create a window in the app when the
  // dock icon is clicked and there are no other windows open.
  await protocolsRegistered;
  window.openMain();
});

ipcMainProxy.on('dashboard-open', () => {
  openDashboard();
});

ipcMainProxy.on('dashboard-close', () => {
  closeDashboard();
});

ipcMainProxy.on('preferences-open', () => {
  openPreferences();
});

ipcMainProxy.on('preferences-close', () => {
  window.getWindow('preferences')?.close();
});

mainEvents.on('dialog-info', (args) => {
  window.getWindow(args.dialog)?.webContents.send('dialog/info', args);
});

ipcMainProxy.on('dialog/error', (event, args) => {
  window.getWindow(args.dialog)?.webContents.send('dialog/error', args);
});

ipcMainProxy.on('dialog/close', (_event, args) => {
  window.getWindow(args.dialog)?.webContents.send('dialog/close', args);
});

// Start and stop Steve as needed
ipcMainProxy.on('backend/app-status-changed', (_event, statusConditions) => {
  const [status] = statusConditions.KubernetesReady ?? [];

  if (status) {
    Steve.getInstance().start().catch((err) => {
      console.error('Failed to start Steve:', err);
    });
  } else {
    Steve.getInstance().stop();
  }
});

ipcMainProxy.handle('versions/macOs', () => {
  return getMacOsVersion();
});

ipcMainProxy.handle('host/isArm', () => {
  return process.arch === 'arm64';
});

ipcMainProxy.on('help/preferences/open-url', async() => {
  try {
    await Help.preferences.openUrl();
  } catch (err) {
    console.error('Failed to open preferences help URL:', err);
  }
});

ipcMainProxy.handle('show-message-box', (_event, options: Electron.MessageBoxOptions): Promise<Electron.MessageBoxReturnValue> => {
  return window.showMessageBox(options, false);
});

ipcMainProxy.handle('show-message-box-rd', async(_event, options: Electron.MessageBoxOptions, modal = false) => {
  const mainWindow = modal ? window.getWindow('main') : null;

  const dialog = window.openDialog(
    'Dialog',
    {
      modal,
      parent: mainWindow || undefined,
      frame:  true,
      title:  options.title,
      height: 225,
    });

  let response: any;

  dialog.webContents.on('ipc-message', (_event, channel, args) => {
    if (channel === 'dialog/mounted') {
      dialog.webContents.send('dialog/options', options);
    }

    if (channel === 'dialog/close') {
      response = args || { response: options.cancelId };
      dialog.close();
    }
  });

  dialog.on('close', () => {
    if (response) {
      return;
    }

    response = { response: options.cancelId };
  });

  await (new Promise<void>((resolve) => {
    dialog.on('closed', resolve);
  }));

  return response;
});

ipcMainProxy.handle('get-locked-fields', () => ({}));

function showErrorDialog(title: string, message: string, fatal?: boolean) {
  if (noModalDialogs) {
    console.log(`Fatal Error:\n${ title }\n\n${ message }`);
  } else {
    Electron.dialog.showErrorBox(title, message);
  }
  if (fatal) {
    Electron.app.quit();
  }
}

function handleFailure(payload: any) {
  let titlePart = 'Error Starting Rancher Desktop';
  let message = 'There was an unknown error starting Rancher Desktop';
  let secondaryMessage = '';

  if (payload instanceof Error) {
    secondaryMessage = payload.toString();
  } else if (typeof payload === 'number') {
    message = `Rancher Desktop was unable to start with the following exit code: ${ payload }`;
  } else if ('errorCode' in payload) {
    message = payload.message || message;
    titlePart = payload.context || titlePart;
  }
  console.log(`Rancher Desktop was unable to start:`, payload);
  if (noModalDialogs) {
    console.log(titlePart);
    console.log(message);
    gone = true;
    Electron.app.quit();
  } else {
    showErrorDialog(titlePart, message, false);
  }
}

/**
 * Checks if Rancher Desktop was run as root.
 */
function isRoot(): boolean {
  const validPlatforms = ['linux', 'darwin'];

  if (!['linux', 'darwin'].includes(os.platform())) {
    throw new Error(`isRoot() can only be called on ${ validPlatforms }`);
  }

  return os.userInfo().uid === 0;
}
