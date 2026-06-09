/*
Copyright © 2026 The Kubernetes Authors
Copyright © 2026 SUSE LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

import { KubeConfig } from './config.js';

export class Watch {
  public static SERVER_SIDE_CLOSE: object = { error: 'Connection closed on server' };
  public config:                   KubeConfig;

  public constructor(config: KubeConfig) {
    this.config = config;
  }

  // Return the timeout for fetch request, in milliseconds.  Kubernetes
  // client-go uses somewhere between 5 and 10 minutes; try to match that here.
  private getRequestTimeoutMS() {
    return 5 * 60 * 1000 + Math.floor(Math.random() * 5 * 60 * 1000);
  }

  // Watch the resource and call provided callback with parsed json object
  // upon event received over the watcher connection.
  //
  // "done" callback is called either when connection is closed or when there
  // is an error. In either case, watcher takes care of properly closing the
  // underlaying connection so that it doesn't leak any resources.
  //
  // This function returns when the connection is established, but does not wait
  // for the body to be read.
  public async watch(
    path: string,
    queryParams: Record<string, string | number | boolean | undefined>,
    callback: (phase: string, apiObj: any, watchObj?: any) => void,
    done: (err: any) => void,
  ): Promise<AbortController> {
    const cluster = this.config.getCurrentCluster();
    if (!cluster) {
      throw new Error('No currently active cluster');
    }
    const watchURL = new URL(cluster.server + path);
    watchURL.searchParams.set('watch', 'true');

    for (const [key, val] of Object.entries(queryParams || {})) {
      if (val !== undefined) {
        watchURL.searchParams.set(key, val.toString());
      }
    }

    const controller = new AbortController();
    const requestInit = this.config.applyToFetchOptions({});
    requestInit.signal = controller.signal;
    requestInit.method = 'GET';

    const timeout = setTimeout(() => {
      // The timeout only applies to the initial connection.
      controller.abort(new DOMException('The operation timed out.', 'TimeoutError'));
    }, this.getRequestTimeoutMS());

    let doneCalled = false;
    const doneCallOnce = (err: any) => {
      if (!doneCalled) {
        doneCalled = true;
        controller.abort();
        done(err);
      }
    };

    try {
      const response = await fetch(watchURL, requestInit);

      if (response.status === 200 && response.body) {
        const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();

        // Read the body in an async function so that we can return the
        // controller immediately to let the caller abort as needed.
        (async() => {
          try {
            let lastLine = '';

            while (true) {
              const { value, done } = await reader.read();
              let match: RegExpExecArray | null;

              lastLine += value ?? '';
              while ((match = /^(.*?)\r?\n(.*)$/s.exec(lastLine))) {
                const [, line, remaining] = match;
                lastLine = remaining;
                let data: any;
                try {
                  data = JSON.parse(line);
                } catch (error) {
                  // ignore parse errors
                  console.debug('Error parsing watch event JSON, ignoring', { line, error });
                  continue;
                }
                callback(data.type, data.object, data);
              }
              if (done) {
                break;
              }
            }
            if (lastLine) {
              let data: any;
              try {
                data = JSON.parse(lastLine);
              } catch (error) {
                console.debug('Error parsing watch event JSON last line, ignoring', { lastLine, error });
              }
              if (data) {
                callback(data.type, data.object, data);
              }
            }
            doneCallOnce(null);
          } catch (err) {
            doneCallOnce(controller.signal.aborted ? controller.signal.reason : err);
          }
        })();
      } else {
        const statusText = response.statusText || 'Internal Server Error';
        const error = new Error(statusText) as Error & {
          statusCode: number | undefined;
        };
        error.statusCode = response.status;
        throw error;
      }
    } catch (err) {
      doneCallOnce(err);
    } finally {
      clearTimeout(timeout);
    }

    return controller;
  }
}
