/**
 * Custom declarations for Electron IPC topics.
 */

import Electron from 'electron';
import semver from 'semver';

import type { Direction, RecursivePartial } from '@pkg/utils/typeUtils';
/**
 * IpcMainEvents describes events the renderer can send to the main process,
 * i.e. ipcRenderer.send() -> ipcMain.on().
 */
export interface IpcMainEvents {
  'settings-read':         () => void;
  'factory-reset':         (keepSystemImages: boolean) => void;
  'update-network-status': (status: boolean) => void;

  // #region backend
  /** The app status has changed.  The input is the status conditions on the app. */
  'backend/app-status-changed': (status: Record<string, readonly [boolean | undefined, string]>) => void;
  /** Trigger the corresponding IPC renderer event. */
  'backend/steve-fetch-port':   () => void;
  // #endregion

  // #region main/update
  'update-state': () => void;
  // Quit and apply the update.
  'update-apply': () => void;
  // #endregion

  // #region dialog
  'dialog/load':    () => void;
  'dialog/ready':   () => void;
  'dialog/mounted': () => void;
  /** For message box only */
  'dialog/error':   (args: Record<string, string>) => void;
  'dialog/close':   (...args: any[]) => void;
  // #endregion

  // #region sudo-prompt
  'sudo-prompt/closed': (suppress: boolean) => void;
  // #endregion

  // #region Preferences
  'preferences-open':       () => void;
  'preferences-close':      () => void;
  'preferences-set-dirty':  (isDirty: boolean) => void;
  'get-debugging-statuses': () => void;
  // #endregion

  'dashboard-open':  () => void;
  'dashboard-close': () => void;

  'diagnostics/run': () => void;

  /** Only for the preferences window */
  'preferences/load': () => void;

  'help/preferences/open-url': () => void;

  // #region Extensions
  'extensions/open':            (id: string, path: string) => void;
  'extensions/close':           () => void;
  'extensions/open-external':   (url: string) => void;
  'extensions/spawn/kill':      (execId: string) => void;
  /** Execute the given command, streaming results back via events. */
  'extensions/spawn/streaming': (
    options: import('@pkg/main/extensions/types').SpawnOptions
  ) => void;
  /** Show a notification */
  'extensions/ui/toast': (
    level: 'success' | 'warning' | 'error',
    message: string
  ) => void;
  'ok:extensions/getContentArea': (payload: { top: number, right: number, bottom: number, left: number }) => void;
  // #endregion
}

/**
 * IpcMainInvokeEvents describes handlers describes RPC calls the renderer can
 * invoke on the main process, i.e. ipcRenderer.invoke() -> ipcMain.handle()
 */
export interface IpcMainInvokeEvents {
  'get-locked-fields':         () => import('@pkg/config/settings').LockedSettingsType;
  'settings-write':            (arg: RecursivePartial<import('@pkg/config/settings').Settings>) => void;
  'transient-settings-fetch':  () => import('@pkg/config/transientSettings').TransientSettings;
  'transient-settings-update': (arg: RecursivePartial<import('@pkg/config/transientSettings').TransientSettings>) => void;
  'show-message-box':          (options: Electron.MessageBoxOptions) => Electron.MessageBoxReturnValue;
  'show-message-box-rd':       (options: Electron.MessageBoxOptions, modal?: boolean) => any;

  // #region extensions
  /** Execute the given command and return the results. */
  'extensions/spawn/blocking': (options: import('@pkg/main/extensions/types').SpawnOptions) => import('@pkg/main/extensions/types').SpawnResult;
  'extensions/ui/show-open':   (options: import('electron').OpenDialogOptions) => import('electron').OpenDialogReturnValue;
  /* Fetch data from the backend, or arbitrary host ignoring CORS. */
  'extensions/vm/http-fetch':  (config: import('@docker/extension-api-client-types').v1.RequestConfig) => import('@docker/extension-api-client-types').v1.ServiceError;
  // #endregion

  // #region Versions
  'versions/macOs': () => semver.SemVer;
  // #endregion

  // #region Host
  'host/isArm': () => boolean;
  // #endregion

  // #region RDD
  /** Fetch the KubeConfig for use with RDD */
  'rdd/kube-config': () => string;
  // #endregion
}

/**
 * IpcRendererEvents describes events that the main process may send to the renderer
 * process, i.e. webContents.send() -> ipcRenderer.on().
 */
export interface IpcRendererEvents {
  'settings-update': (
    settings: import('@pkg/config/settings').Settings
  ) => void;
  'settings-read':             (settings: import('@pkg/config/settings').Settings) => void;
  'update-state':              (state: import('@pkg/main/update').UpdateState) => void;
  'always-debugging':          (status: boolean) => void;
  'is-debugging':              (status: boolean) => void;
  /** The steve port may have changed. */
  'backend/steve-port':        (port: number) => void;
  'kubernetes-errors-details': (
    titlePart: string,
    mainMessage: string,
  ) => void;
  'update-network-status': (status: boolean) => void;
  'diagnostics/update':    () => void;

  // #region dialog
  'dialog/mounted':  () => void;
  'dialog/populate': (...args: any) => void;
  'dialog/size':     (size: { width: number; height: number }) => void;
  'dialog/options':  (...args: any) => void;
  'dialog/close':    (...args: any) => void;
  'dialog/error':    (args: any) => void;
  'dialog/info':     (args: Record<string, string>) => void;
  'dashboard-open':  () => void;
  // #endregion

  // #region tab navigation
  route: (route: {
    name?:      string;
    path?:      string;
    direction?: Direction;
  }) => void;
  // #endregion

  // #region extensions
  // The list of installed extensions may have changed.
  'extensions/changed':        () => void;
  'extensions/getContentArea': () => void;
  'extensions/open':           (id: string, path: string) => void;
  'err:extensions/open':       () => void;
  'extensions/close':          () => void;
  'extensions/spawn/close':    (id: string, code: number) => void;
  'extensions/spawn/error':    (id: string, error: Error | NodeJS.Signals) => void;
  'extensions/spawn/output': (
    id: string,
    data: { stdout: string } | { stderr: string }
  ) => void;
  'ok:extensions/uninstall': (id: string) => void;
  // #endregion

  // #region window
  'window/blur': (state: boolean) => void;
  // #endregion

  // #region preferences
  'preferences/changed': () => void;
  // #endregion

  // #region Versions
  'versions/macOs': (macOsVersion: semver.SemVer) => void;
  // #endregion

  // #region Host
  'host/isArm': (isArm: boolean) => void;
  // #endregion
}
