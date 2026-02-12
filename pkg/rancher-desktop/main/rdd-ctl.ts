// This file contains handlers to let the front end use `rdd ctl`.
import fs from 'node:fs';
import path from 'node:path';

import Electron from 'electron';
import which from 'which';

import { getIpcMainProxy } from '@pkg/main/ipcMain';
import mainEvents from '@pkg/main/mainEvents';
import { spawnFile } from '@pkg/utils/childProcess';
import Logging from '@pkg/utils/logging';

const console = Logging.rdd;

let lastKubeConfig = '';

/**
 * fetchConfig returns the kubeconfig for accessing RDD.  If it has previously
 * been fetched, it returns the cached version.
 * @note If we fail to fetch the configuration, we just wait more instead of
 * throwing.
 */
async function fetchConfig(): Promise<string> {
  if (lastKubeConfig) {
    // Return any existing kubeconfig that is available.
    return lastKubeConfig;
  }
  const srcDir = path.dirname(Electron.app.getAppPath());
  const exeName = process.platform === 'win32' ? 'rdd.exe' : 'rdd';
  let rddPath = path.join(srcDir, 'bin', exeName);

  try {
    await fs.promises.access(rddPath, fs.constants.X_OK);
  } catch {
    rddPath = await which('rdd');
  }

  // Loop until the control plane is ready.
  while (true) {
    try {
      const { stdout } = await spawnFile(
        rddPath,
        ['service', 'config'],
        { stdio: ['ignore', 'pipe', console] });

      // Assume error messages do not contain `apiVersion`; as we control the
      // output, that should be safe.
      if (stdout.includes('apiVersion')) {
        lastKubeConfig = stdout;

        return stdout;
      }
    } catch (err) {
      console.error('Error fetching kube config, retrying', err);
    }

    await new Promise(resolve => setTimeout(resolve, 1_000));
  }
}

getIpcMainProxy(console).handle('rdd/kube-config', () => fetchConfig());
mainEvents.handle('rdd/kube-config', () => fetchConfig());
