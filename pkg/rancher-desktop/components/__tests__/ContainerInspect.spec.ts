// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { createStore } from 'vuex';

import ContainerInspect from '@pkg/components/ContainerInspect.vue';
import { switchableT, t } from '@pkg/utils/testUtils/translations';

describe('ContainerInspect', () => {
  it('shows the fallback error text in the current locale', async() => {
    const translator = switchableT();
    const store = createStore({
      state:   { 'container-engine': { inspectData: {} } },
      getters: { 'i18n/t': () => translator.t },
      actions: { 'container-engine/fetchContainerInspect': () => Promise.reject({}) },
    });
    const wrapper = mount(ContainerInspect, {
      global: { plugins: [store] },
      props:  { containerId: 'web', namespace: undefined },
    });

    await flushPromises();
    translator.switched.value = true;
    await nextTick();
    expect(wrapper.get('[data-testid="info-error"]').text()).toBe(`[${ t('containerInspect.error.loadFailed') }]`);
  });
});
