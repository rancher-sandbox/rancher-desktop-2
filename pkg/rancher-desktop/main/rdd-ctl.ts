// This file contains handlers to let the front end use `rdd ctl`.
import stream from 'node:stream';

import Electron from 'electron';

import { getIpcMainProxy } from '@pkg/main/ipcMain';
import mainEvents from '@pkg/main/mainEvents';
import { spawnFile, SpawnError } from '@pkg/utils/childProcess';
import Latch from '@pkg/utils/latch';
import Logging from '@pkg/utils/logging';
import { getRDDPath } from '@pkg/utils/paths';

const console = Logging.rdd;
const CodeIncompatibleServer = 5;

const certificateCallback = Latch<(kubeConfig: string) => void>();
let fetchConfigPromise: Promise<string> | undefined;

mainEvents.on('rdd/certificate-callback', certificateCallback.resolve);

/**
 * fetchConfig returns the kubeconfig for accessing RDD.
 * @note Using fetchConfig is preferred.
 * @note If we fail to fetch the configuration, we just wait more instead of
 * throwing.
 */
async function fetchConfigWithoutCache(): Promise<string> {
  const rddPath = getRDDPath();

  // Loop until the control plane is ready.
  while (true) {
    // stderr from the `service config` command, in case there is a JSON error
    // message we can parse.
    let stderr = '';
    let lastStderrLine = '';
    // stderrWritable is a Writable that logs to the `stderr` variable, and also
    // dumps the value to the console.
    const stderrWritable = new stream.Writable({
      write(chunk, encoding, callback) {
        let error: Error | null = null;
        try {
          stderr += chunk.toString();
          const lines = (lastStderrLine + chunk.toString()).split('\n');
          lastStderrLine = lines.pop() ?? '';
          for (const line of lines) {
            console.error(line);
          }
        } catch (err) {
          error = err as Error;
        }
        callback(error);
      },
      final(callback) {
        let error: Error | null = null;
        try {
          if (lastStderrLine) {
            console.error(lastStderrLine);
          }
        } catch (err) {
          error = err as Error;
        }
        callback(error);
      },
    });
    try {
      const { stdout } = await spawnFile(
        rddPath,
        ['service', 'config', '--log-format=json'],
        { stdio: ['ignore', 'pipe', stderrWritable], encoding: 'utf-8' });

      // Assume all other error messages do not contain `apiVersion`; as we
      // control the output, that should be safe.
      if (stdout.includes('apiVersion')) {
        // Notify the networking code that the kubeconfig is ready, to configure
        // the certificate handling.  This must happen before we return so that
        // the caller will not get certificate errors when making requests.
        try {
          (await certificateCallback)(stdout);
        } catch (err) {
          // Retrying to get the config will not help; just log the error so we
          // can debug it later.  The user will see other errors and need to
          // restart.
          console.error('Error processing new RDD configuration', err);
        }

        // Log the RDD version for debugging purposes; no need to do that
        // synchronously, though.
        spawnFile(rddPath, ['--version=raw'], { stdio: ['ignore', 'pipe', console] })
          .then(({ stdout }) => console.log('RDD version:', stdout))
          .catch(err => console.error('Error fetching RDD version:', err));

        return stdout;
      }
    } catch (err) {
      if (err instanceof SpawnError && err.code === CodeIncompatibleServer) {
        // Fatal: the server is using an incompatible backend.  `stderr` is in
        // JSONL (i.e. JSON objects on separate lines); try to get the error
        // message out of them.
        let message = 'Unsupported RDD service version';
        for (const line of stderr.split(/\r?\n/)) {
          try {
            const error: { 'exit-code'?: number; msg?: string } = JSON.parse(line);
            if (error?.['exit-code'] !== CodeIncompatibleServer) {
              continue;
            }
            if (typeof error?.msg === 'string' && error.msg && error.msg !== '<nil>') {
              message = error.msg;
              break;
            }
          } catch { /* ignore; try next line, or use fallback message */ }
        }
        console.error(message);
        Electron.dialog.showErrorBox('Incompatible RDD Service', message);
        Electron.app.quit();
        // We cannot recover (the user needs to restart the application), so
        // `fetchConfig` caching the error is fine.  However, do not use the
        // original error; the user seeing this does not need to see the command
        // line that caused the issue.
        // eslint-disable-next-line preserve-caught-error
        throw new Error(message);
      }
      console.error('Error fetching kube config, retrying', err);
    }

    await new Promise(resolve => setTimeout(resolve, 1_000));
  }
}

/**
 * fetchConfig returns the kubeconfig for accessing RDD.  If it has previously
 * been fetched, it returns the cached version.
 * @note If we fail to fetch the configuration, we just wait more instead of
 * throwing.
 */
function fetchConfig(): Promise<string> {
  fetchConfigPromise ??= fetchConfigWithoutCache();

  return fetchConfigPromise;
}

getIpcMainProxy(console).handle('rdd/kube-config', () => fetchConfig());
mainEvents.handle('rdd/kube-config', () => fetchConfig());
