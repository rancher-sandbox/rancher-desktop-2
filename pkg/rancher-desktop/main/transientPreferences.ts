import _ from 'lodash';

import { getIpcMainProxy } from '@pkg/main/ipcMain';
import mainEvents from '@pkg/main/mainEvents';
import defaultTransientPreferences from '@pkg/types/transientPreferences';
import Logging from '@pkg/utils/logging';
import { send } from '@pkg/window';

const console = Logging.transientPreferences;
const ipcMainProxy = getIpcMainProxy(console);

/**
 * transientPreferences is the current transient preferences state.
 */
const transientPreferences = structuredClone(defaultTransientPreferences);

export default function initializeTransientPreferences() {
  mainEvents.handle('transient-preferences/get', () => {
    return Promise.resolve(transientPreferences);
  });
  ipcMainProxy.on('transient-preferences/get', () => {
    send('transient-preferences/update', transientPreferences);
  });

  ipcMainProxy.handle('transient-preferences/set', (_event, preferences) => {
    _.merge(transientPreferences, preferences);
    send('transient-preferences/update', transientPreferences);
  });
}
