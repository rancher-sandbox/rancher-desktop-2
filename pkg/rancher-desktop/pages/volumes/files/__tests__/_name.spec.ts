// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { jest } from '@jest/globals';
import { shallowMount } from '@vue/test-utils';
import { nextTick } from 'vue';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { switchableT, t } from '@pkg/utils/testUtils/translations';

mockModules({
  '@pkg/entry/store':       { mapTypedState: () => ({}) },
  '@pkg/utils/ipcRenderer': { ipcRenderer: { on: jest.fn(), send: jest.fn(), removeListener: jest.fn() } },
});

const { default: VolumeFiles } = await import('@pkg/pages/volumes/files/_name.vue');

describe('volume files', () => {
  /** Mount the page for the given volume name, translating with the given t. */
  function mountPage(name: string, translate: typeof t) {
    return shallowMount(VolumeFiles, {
      global: {
        mocks: {
          $route: { params: { name }, query: {} },
          $store: { dispatch: jest.fn() },
          t:      translate,
        },
        stubs: { Banner: { template: '<div class="banner"><slot /></div>' } },
      },
    });
  }

  it('shows an error instead of loading forever for an invalid volume name', async() => {
    const wrapper = mountPage('no/such volume', t);

    await nextTick();
    expect(wrapper.find('.banner').text()).toContain(t('volumes.files.invalidVolumeName', { name: 'no/such volume' }));
    expect(wrapper.findComponent({ name: 'LoadingIndicator' }).exists()).toBe(false);
  });

  it('shows its error in the current locale', async() => {
    const { t: tt, switchLocale } = switchableT();
    const wrapper = mountPage('no/such volume', tt);

    await switchLocale();
    expect(wrapper.text()).toContain(tt('volumes.files.invalidVolumeName', { name: 'no/such volume' }));
  });

  it('translates its headers after a locale switch', async() => {
    const { t: tt, switchLocale } = switchableT();
    const wrapper = mountPage('data', tt);

    // Show the file table instead of the loading indicator.
    await wrapper.setData({ isLoading: false });
    await switchLocale();
    expect(wrapper.getComponent({ name: 'SortableTable' }).props('headers'))
      .toContainEqual(expect.objectContaining({ label: tt('volumes.files.table.header.modified') }));
    wrapper.unmount();
  });
});
