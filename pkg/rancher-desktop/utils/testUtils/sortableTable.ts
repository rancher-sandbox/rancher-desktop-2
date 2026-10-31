// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

/**
 * Mount options for tests that render a SortableTable.
 */

import { jest } from '@jest/globals';

import { t } from './translations';

/**
 * The `global` mount options shared by the specs that render a SortableTable,
 * plus any extra mocks.
 */
export function sortableTableGlobal(mocks: Record<string, unknown> = {}) {
  return {
    directives: {
      'clean-html':      {},
      'clean-tooltip':   {},
      'close-popper':    {},
      shortkey:          {},
      tooltip:           {},
      'trim-whitespace': {},
    },
    mocks: {
      $store: {
        getters:  {
          'resource-fetch/isTooManyItemsToAutoUpdate': false,
          'resource-fetch/manualRefreshIsLoading':     false,
        },
        commit:   jest.fn(),
        dispatch: jest.fn(),
      },
      t,
      ...mocks,
    },
    stubs: {
      T: { template: '<span></span>' },
    },
  };
}
