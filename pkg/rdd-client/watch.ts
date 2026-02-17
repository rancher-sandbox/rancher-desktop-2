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

  // Watch the resource and call provided callback with parsed json object
  // upon event received over the watcher connection.
  //
  // "done" callback is called either when connection is closed or when there
  // is an error. In either case, watcher takes care of properly closing the
  // underlaying connection so that it doesn't leak any resources.
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

    const requestInit = this.config.applyToFetchOptions({});

    const controller = new AbortController();
    const timeoutSignal = AbortSignal.timeout(30000);
    requestInit.signal = AbortSignal.any([controller.signal, timeoutSignal]);
    requestInit.method = 'GET';

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
        let lastLine = '';

        while (true) {
          const { value, done } = await reader.read();
          let match: RegExpExecArray | null;

          lastLine += value ?? '';
          while ((match = /^(.*?)\r?\n(.*?)$/.exec(lastLine))) {
            const [, line, remaining] = match;
            lastLine = remaining;
            try {
              const data = JSON.parse(line);
              callback(data.type, data.object, data);
            } catch {
              // ignore parse errors
            }
          }
          if (done) {
            break;
          }
        }
        doneCallOnce(null);
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
    }

    return controller;
  }
}
