// This import is for the tray found in the menu bar (upper right on macos or
// lower right on Windows).

import fs from 'fs';
import os from 'os';
import path from 'path';

import { KubeConfig } from '@kubernetes/client-node';
import Electron from 'electron';

import { Settings } from '@pkg/config/settings';
import { getIpcMainProxy } from '@pkg/main/ipcMain';
import mainEvents from '@pkg/main/mainEvents';
import { checkConnectivity } from '@pkg/main/networking';
import Logging from '@pkg/utils/logging';
import { networkStatus } from '@pkg/utils/networks';
import paths from '@pkg/utils/paths';
import { openMain, send } from '@pkg/window';

const console = Logging.background;
const ipcMainProxy = getIpcMainProxy(console);

/**
 * Tray is a class to manage the tray icon for rancher-desktop.
 */
export class Tray {
  protected trayMenu:              Electron.Tray;
  protected backendIsLocked = '';
  private settings:                Settings;
  private currentNetworkStatus:    networkStatus = networkStatus.CHECKING;
  private static instance:         Tray;
  private networkState:            boolean | undefined;
  private runBuildFromConfigTimer: NodeJS.Timeout | null = null;
  private kubeConfigWatchers:      fs.FSWatcher[] = [];

  protected contextMenuItems: Electron.MenuItemConstructorOptions[] = [
    {
      id:      'state',
      enabled: false,
      label:   'Kubernetes is starting',
      type:    'normal',
      icon:    path.join(paths.resources, 'icons', 'kubernetes-icon-black.png'),
    },
    {
      id:      'network-status',
      enabled: false,
      label:   `Network status: ${ this.currentNetworkStatus }`,
      type:    'normal',
      icon:    '',
    },
    /* TODO: https://github.com/rancher-sandbox/rancher-desktop-app/issues/26
    {
      id:      'container-engine',
      enabled: false,
      label:   '?',
      type:    'normal',
      icon:    '',
    },
    */
    { type: 'separator' },
    {
      id:    'main',
      label: 'Open main window',
      type:  'normal',
      click() {
        openMain();
      },
    },
    /* TODO: https://github.com/rancher-sandbox/rancher-desktop-app/issues/26
    {
      id:    'preferences',
      label: 'Open preferences dialog',
      type:  'normal',
      click: openPreferences,
    },
    */
    /* TODO: https://github.com/rancher-sandbox/rancher-desktop-app/issues/27
    {
      id:      'dashboard',
      enabled: false,
      label:   'Open cluster dashboard',
      type:    'normal',
      click:   openDashboard,
    },
    { type: 'separator' },
    */
    /* TODO: https://github.com/rancher-sandbox/rancher-desktop-app/issues/39
    {
      id:      'contexts',
      label:   'Kubernetes Contexts',
      type:    'submenu',
      submenu: [],
    },
    */
    { type: 'separator' },
    {
      id:    'quit',
      label: `Quit ${ Electron.app.name }`,
      role:  'quit',
      type:  'normal',
    },
  ];

  private isMacOs = () => {
    return os.platform() === 'darwin';
  };

  private isLinux = () => {
    return os.platform() === 'linux';
  };

  private readonly trayIconsMacOs = {
    stopped:  path.join(paths.resources, 'icons', 'logo-tray-stopped-Template@2x.png'),
    starting: path.join(paths.resources, 'icons', 'logo-tray-starting-Template@2x.png'),
    started:  path.join(paths.resources, 'icons', 'logo-tray-Template@2x.png'),
    stopping: path.join(paths.resources, 'icons', 'logo-tray-stopping-Template@2x.png'),
    error:    path.join(paths.resources, 'icons', 'logo-tray-error-Template@2x.png'),
  };

  private readonly trayIcons = {
    stopped:  '',
    starting: path.join(paths.resources, 'icons', 'logo-square-bw.png'),
    started:  path.join(paths.resources, 'icons', 'logo-square.png'),
    stopping: '',
    error:    path.join(paths.resources, 'icons', 'logo-square-red.png'),
  };

  private readonly trayIconSet = this.isMacOs() ? this.trayIconsMacOs : this.trayIcons;

  private constructor(settings: Settings) {
    this.settings = settings;
    this.trayMenu = new Electron.Tray(this.trayIconSet.starting);
    this.trayMenu.setToolTip(Electron.app.name);
    const menuItem = this.contextMenuItems.find(item => item.id === 'container-engine');

    if (menuItem) {
      menuItem.label = `Container engine: ${ this.settings.containerEngine.name }`;
    }

    // Discover k8s contexts
    try {
      this.updateContexts();
    } catch (err) {
      Electron.dialog.showErrorBox('Error starting the app:',
        `Error message: ${ err instanceof Error ? err.message : err }`);
    }

    const contextMenu = Electron.Menu.buildFromTemplate(this.contextMenuItems);

    this.trayMenu.setContextMenu(contextMenu);

    this.buildFromConfig();

    mainEvents.on('backend-locked-update', this.backendStateEvent);
    mainEvents.emit('backend-locked-check');

    // If the network connectivity diagnostic changes results, update it here.
    mainEvents.on('diagnostics-event', payload => {
      if (payload.id !== 'network-connectivity') {
        return;
      }

      const { connected } = payload;

      if (this.networkState === connected) {
        return; // network state hasn't changed since last check
      }

      this.networkState = connected;

      this.handleUpdateNetworkStatus(this.networkState).catch((err: any) => {
        console.log('Error updating network status: ', err);
      });
    });
  }

  private backendStateEvent = (backendIsLocked: string) => {
    this.backendStateChanged(backendIsLocked);
  };

  private settingsUpdateEvent = (cfg: Settings) => {
    this.settings = cfg;
    this.settingsChanged();
  };

  private updateNetworkStatusEvent = (_: Electron.IpcMainEvent, status: boolean) => {
    this.handleUpdateNetworkStatus(status).catch((err:any) => {
      console.log('Error updating network status: ', err);
    });
  };

  /**
   * Checks for an existing instance of Tray. If one does not
   * exist, instantiate a new one.
   */
  public static getInstance(settings: Settings): Tray {
    Tray.instance ??= new Tray(settings);

    return Tray.instance;
  }

  /**
   * Hide the tray menu.
   */
  public hide() {
    this.trayMenu.destroy();
    ipcMainProxy.removeListener('update-network-status', this.updateNetworkStatusEvent);
    if (this.runBuildFromConfigTimer) {
      clearTimeout(this.runBuildFromConfigTimer);
      this.runBuildFromConfigTimer = null;
    }
    for (const watcher of this.kubeConfigWatchers) {
      watcher.close();
    }
    this.kubeConfigWatchers = [];
  }

  /**
   * Show the tray menu.
   */
  public show() {
    if (this.trayMenu.isDestroyed()) {
      Tray.instance = new Tray(this.settings);
    }
  }

  protected async handleUpdateNetworkStatus(status: boolean) {
    if (!status) {
      this.currentNetworkStatus = networkStatus.OFFLINE;
    } else {
      this.currentNetworkStatus = await checkConnectivity('k3s.io') ? networkStatus.CONNECTED : networkStatus.OFFLINE;
    }
    mainEvents.emit('update-network-status', this.currentNetworkStatus === networkStatus.CONNECTED);
    send('update-network-status', this.currentNetworkStatus === networkStatus.CONNECTED);
    this.updateMenu();
  }

  protected buildFromConfig() {
    try {
      this.updateContexts();
      const contextMenu = Electron.Menu.buildFromTemplate(this.contextMenuItems);

      this.trayMenu.setContextMenu(contextMenu);
    } catch (err) {
      console.log(`Error trying to update context menu: ${ err }`);
    }
  }

  protected backendStateChanged(backendIsLocked: string) {
    this.backendIsLocked = backendIsLocked;
    this.updateMenu();
  }

  /**
   * Called when the application settings have changed.
   */
  protected settingsChanged() {
    this.updateMenu();
  }

  protected updateMenu() {
    if (this.trayMenu.isDestroyed()) {
      return;
    }

    const logo = this.trayIconSet.starting;

    // TODO: Update the tray icon and state based on backend state.

    const containerEngineMenu = this.contextMenuItems.find(item => item.id === 'container-engine');

    if (containerEngineMenu) {
      const containerEngine = this.settings.containerEngine.name;

      containerEngineMenu.label = containerEngine === 'containerd' ? containerEngine : `dockerd (${ containerEngine })`;
      containerEngineMenu.icon = containerEngine === 'containerd' ? path.join(paths.resources, 'icons', 'containerd-icon-color.png') : '';
    }
    const networkStatusItem = this.contextMenuItems.find(item => item.id === 'network-status');

    if (networkStatusItem) {
      networkStatusItem.label = `Network status: ${ this.currentNetworkStatus }`;
    }

    this.contextMenuItems
      .filter(item => item.id && ['preferences', 'dashboard', 'contexts', 'quit'].includes(item.id))
      .forEach((item) => {
        item.enabled = !this.backendIsLocked;
      });

    const contextMenu = Electron.Menu.buildFromTemplate(this.contextMenuItems);

    this.trayMenu.setContextMenu(contextMenu);
    this.trayMenu.setImage(logo);
  }

  protected updateDashboardState = (enabled = true) => this.contextMenuItems
    .map(item => item.id === 'dashboard' ? { ...item, enabled } : item);

  /**
   * Update the list of Kubernetes contexts in the tray menu.
   * This does _not_ raise any exceptions if we fail to read the config.
   */
  protected updateContexts() {
    const kc = new KubeConfig();

    try {
      kc.loadFromDefault();
    } catch (ex) {
      console.error('Failed to load kubeconfig, ignoring:', ex);
      // Keep going, with no context set.
    }

    const contextsMenu = this.contextMenuItems.find(item => item.id === 'contexts');
    const curr = kc.getCurrentContext();
    const ctxs = kc.getContexts();

    if (!contextsMenu) {
      return;
    }
    if (ctxs.length === 0) {
      contextsMenu.submenu = [{ label: 'None found' }];
    } else {
      contextsMenu.submenu = ctxs.map(val => ({
        label:   val.name,
        type:    'checkbox',
        click:   menuItem => this.contextClick(menuItem),
        checked: (val.name === curr),
      }));
    }
  }

  /**
   * Call back when a menu item is clicked to change the active Kubernetes context.
   * @param {Electron.MenuItem} menuItem The menu item that was clicked.
   */
  protected contextClick(menuItem: Electron.MenuItem) {
    // TODO: Implement this
  }
}
