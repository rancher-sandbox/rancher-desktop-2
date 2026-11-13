// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { jest } from '@jest/globals';
import { shallowMount } from '@vue/test-utils';
import { nextTick } from 'vue';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { t } from '@pkg/utils/testUtils/translations';

mockModules({
  '@pkg/entry/store':       { mapTypedState: () => ({}) },
  '@pkg/utils/ipcRenderer': { ipcRenderer: { on: jest.fn(), send: jest.fn(), removeListener: jest.fn() } },
});

const { default: VolumeFiles } = await import('@pkg/pages/volumes/files/_name.vue');

describe('volume files', () => {
  it('shows an error instead of loading forever for an invalid volume name', async() => {
    const wrapper = shallowMount(VolumeFiles, {
      global: {
        mocks: {
          $route: { params: { name: 'no/such volume' }, query: {} },
          $store: { dispatch: jest.fn() },
          t,
        },
        stubs: { Banner: { template: '<div class="banner"><slot /></div>' } },
      },
    });

    await nextTick();
    expect(wrapper.find('.banner').text()).toContain(t('volumes.files.invalidVolumeName', { name: 'no/such volume' }));
    expect(wrapper.findComponent({ name: 'LoadingIndicator' }).exists()).toBe(false);
  });
});
