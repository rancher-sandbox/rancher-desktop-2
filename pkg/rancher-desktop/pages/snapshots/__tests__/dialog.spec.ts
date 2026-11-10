// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { jest } from '@jest/globals';
import { shallowMount } from '@vue/test-utils';
import { nextTick } from 'vue';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { switchableT, t } from '@pkg/utils/testUtils/translations';

const listeners: Record<string, (event: unknown, args: any) => void> = {};

mockModules({
  '@pkg/utils/ipcRenderer': {
    ipcRenderer: {
      on(channel: string, listener: (event: unknown, args: any) => void) {
        listeners[channel] = listener;
      },
      send:               jest.fn(),
      removeAllListeners: jest.fn(),
    },
  },
});

const { default: SnapshotsDialog } = await import('@pkg/pages/snapshots/dialog.vue');

describe('snapshots dialog', () => {
  it('shows its info in the current locale', async() => {
    const translator = switchableT();
    const wrapper = shallowMount(SnapshotsDialog, {
      global: {
        directives: { 'clean-html': (el: HTMLElement, { value }: { value: string }) => { el.textContent = value } },
        mocks:      { t: translator.t },
        stubs:      { Banner: { template: '<div><slot /></div>' } },
      },
    });

    listeners['dialog/info'](null, { infoKey: 'snapshots.dialog.showLogs' });
    translator.switched.value = true;
    await nextTick();
    expect(wrapper.find('.info').text()).toBe(`[${ t('snapshots.dialog.showLogs') }]`);
  });
});
