/**
 * This file declares our use of `cookie-universal` in the Vuex store; see
 * {@link ../entry/index.ts} for the actual implementation.
 */

import type { ICookie } from 'cookie-universal';

declare module 'vuex' {
  interface Store<S> {
    $cookies: ICookie;
  }
}
