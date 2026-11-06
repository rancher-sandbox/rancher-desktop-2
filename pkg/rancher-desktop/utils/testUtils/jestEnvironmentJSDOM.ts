/**
 * Copyright (c) Meta Platforms, Inc. and affiliates.
 * Copyright Contributors to the Jest project.
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

import BaseEnv from '@jest/environment-jsdom-abstract';
import * as JSDOM from 'jsdom';

import type {
  EnvironmentContext,
  JestEnvironmentConfig,
} from '@jest/environment';

export default class JSDOMEnvironment extends BaseEnv {
  constructor(config: JestEnvironmentConfig, context: EnvironmentContext) {
    super(config, context, JSDOM);

    // jsdom's LocalStorage implementation actually implements the origin check
    // to only allow it on HTTP-ish origins.  This interacts badly with our
    // preload script, which looks for `app://` URLs to inject.  Work around
    // this by examining the origin first, and overriding it if necessary.  Note
    // that jest resets mocks by enumerating everything before the tests start,
    // so we need this even though we don't actually use LocalStorage ourselves.
    if (this.dom) {
      const { window } = this.dom;
      const document = window._document as { _origin: string };
      if (document._origin === 'null' && window.location.protocol === 'app:') {
        document._origin = 'https://rancher-desktop.test';
      }
    }
  }
}
