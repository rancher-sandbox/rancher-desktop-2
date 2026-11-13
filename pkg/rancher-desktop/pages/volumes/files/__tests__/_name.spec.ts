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
  /**
   * Mount the page for the given volume name, translating with the given t,
   * and show its content instead of the loading indicator.
   */
  async function mountPage(name: string, translate: typeof t) {
    const wrapper = shallowMount(VolumeFiles, {
      global: {
        mocks: {
          $route: { params: { name }, query: {} },
          $store: { dispatch: jest.fn() },
          t:      translate,
        },
        stubs: { Banner: { template: '<div><slot /></div>' } },
      },
    });

    await wrapper.setData({ isLoading: false });

    return wrapper;
  }

  it('shows its error in the current locale', async() => {
    const translator = switchableT();
    const wrapper = await mountPage('no/such volume', translator.t);

    translator.switched.value = true;
    await nextTick();
    expect(wrapper.text()).toContain(`[${ t('volumes.files.invalidVolumeName', { name: 'no/such volume' }) }]`);
  });

  it('translates its headers after a locale switch', async() => {
    const translator = switchableT();
    const wrapper = await mountPage('data', translator.t);

    translator.switched.value = true;
    await nextTick();
    expect(wrapper.getComponent({ name: 'SortableTable' }).props('headers'))
      .toContainEqual(expect.objectContaining({ label: `[${ t('volumes.files.table.header.modified') }]` }));
    wrapper.unmount();
  });
});
