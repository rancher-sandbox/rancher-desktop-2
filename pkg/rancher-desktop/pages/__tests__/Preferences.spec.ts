// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { jest } from '@jest/globals';
import { shallowMount } from '@vue/test-utils';
import { createStore } from 'vuex';

import mockModules from '@pkg/utils/testUtils/mockModules';

const componentStub = { template: '<div />' };

const { '@pkg/utils/ipcRenderer': { ipcRenderer } } = mockModules({
  '@pkg/components/EmptyState.vue':              componentStub,
  '@pkg/components/Preferences/ModalBody.vue':   componentStub,
  '@pkg/components/Preferences/ModalFooter.vue': { name: 'preferences-footer', template: '<div />' },
  '@pkg/components/Preferences/ModalHeader.vue': componentStub,
  '@pkg/components/Preferences/ModalNav.vue':    componentStub,
  '@pkg/utils/ipcRenderer':                      {
    ipcRenderer: {
      on:             jest.fn(),
      send:           jest.fn(),
      removeListener: jest.fn(),
    },
  },
});

const i18n = await import('@pkg/store/i18n');
const { default: Preferences } = await import('@pkg/pages/Preferences.vue');

describe('Preferences window', () => {
  let changes: Record<string, unknown>;

  async function mountPreferences() {
    changes = {};
    const store = createStore({
      modules: {
        i18n: {
          namespaced: true, state: i18n.state, getters: i18n.getters, mutations: i18n.mutations, actions: i18n.actions,
        },
        preferences: {
          namespaced: true,
          state:      () => ({ changes }),
          getters:    { committed: () => ({ application: { locale: 'en-us' } }) },
          // The commit succeeds before the committed state reaches this
          // window, so the changes and the previewed locale remain.
          actions:    { commit: () => true, clear: () => {} },
        },
        'transient-preferences': {
          namespaced: true,
          state:      () => ({ navigation: { preferences: { top: 'Application' } } }),
        },
        rdd: {
          namespaced: true,
          actions:    { watchResources: () => {}, unwatchResources: () => {} },
        },
      },
    });

    await store.dispatch('i18n/init');
    await store.dispatch('i18n/switchTo', 'de');
    ipcRenderer.send.mockClear();
    jest.spyOn(window, 'close').mockImplementation(() => {});

    return shallowMount(Preferences, { global: { plugins: [store] } });
  }

  function beforeUnloadCancelled() {
    const event = new Event('beforeunload', { cancelable: true });

    window.dispatchEvent(event);

    return event.defaultPrevented;
  }

  function expectLocaleRestored(restored: boolean) {
    window.dispatchEvent(new Event('pagehide'));
    expect(localStorage.getItem('locale')).toBe(restored ? 'en-us' : 'de');
    if (restored) {
      expect(ipcRenderer.send).toHaveBeenCalledWith('i18n/locale-change', 'en-us');
    } else {
      expect(ipcRenderer.send).not.toHaveBeenCalled();
    }
  }

  it('asks before closing only with uncommitted changes', async() => {
    const wrapper = await mountPreferences();

    expect(beforeUnloadCancelled()).toBe(false);
    changes['application.locale'] = 'de';
    expect(beforeUnloadCancelled()).toBe(true);
    wrapper.unmount();
  });

  it('restores the committed locale when the page goes away', async() => {
    const wrapper = await mountPreferences();

    expectLocaleRestored(true);
    wrapper.unmount();
  });

  it('does nothing on close after a successful apply', async() => {
    const wrapper = await mountPreferences();

    changes['application.locale'] = 'de';
    await wrapper.findComponent({ name: 'preferences-footer' }).vm.$emit('apply');
    await new Promise(resolve => setTimeout(resolve));
    expect(window.close).toHaveBeenCalled();
    expect(beforeUnloadCancelled()).toBe(false);
    expectLocaleRestored(false);
    wrapper.unmount();
  });

  it('does nothing after unmount', async() => {
    const wrapper = await mountPreferences();

    changes['application.locale'] = 'de';
    wrapper.unmount();
    expect(beforeUnloadCancelled()).toBe(false);
    expectLocaleRestored(false);
  });
});
