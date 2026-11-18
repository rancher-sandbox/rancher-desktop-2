// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { jest } from '@jest/globals';
import { flushPromises, shallowMount } from '@vue/test-utils';
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
      invoke:         jest.fn<(channel: string, ...args: any[]) => Promise<{ response: number }>>(),
      removeListener: jest.fn(),
    },
  },
});

const { default: Preferences } = await import('@pkg/pages/Preferences.vue');

describe('Preferences window', () => {
  let wrapper: ReturnType<typeof shallowMount> | undefined;

  afterEach(() => {
    wrapper?.unmount();
    wrapper = undefined;
  });

  function mountPreferences(commitSucceeds = true) {
    const store = createStore<any>({
      modules: {
        i18n: {
          namespaced: true,
          getters:    { t: () => (key: string) => key },
          actions:    { switchTo: jest.fn() },
        },
        preferences: {
          namespaced: true,
          state:      () => ({ changes: {} as Record<string, unknown> }),
          getters:    {
            committed: () => ({ application: { locale: 'en-us' } }),
            isDirty:   state => Object.keys(state.changes).length > 0,
          },
          mutations: { SET_CHANGES: (state, changes) => { state.changes = changes } },
          // By default the commit succeeds before the updated App reaches this
          // window, so the applied values remain in `changes`.
          actions:   {
            commit: () => commitSucceeds,
            clear:  ({ commit }) => commit('SET_CHANGES', {}),
          },
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

    ipcRenderer.invoke.mockReset().mockResolvedValue({ response: 1 });
    jest.spyOn(window, 'close').mockReset().mockImplementation(() => {});
    store.commit('preferences/SET_CHANGES', { 'application.locale': 'de' });

    wrapper = shallowMount(Preferences, { global: { plugins: [store] } });

    return store;
  }

  function beforeUnloadCancelled() {
    const event = new Event('beforeunload', { cancelable: true });

    window.dispatchEvent(event);

    return event.defaultPrevented;
  }

  it.each([
    ['Discard', 0, {}, 1],
    ['Cancel', 1, { 'application.locale': 'de' }, 0],
  ])('asks before dropping unsaved changes (%s)', async(_name, response, changes, closes) => {
    const store = mountPreferences();

    ipcRenderer.invoke.mockResolvedValue({ response });
    expect(beforeUnloadCancelled()).toBe(true);
    expect(ipcRenderer.invoke).toHaveBeenCalledWith('show-message-box', expect.objectContaining({
      message: 'preferencesWindow.close.message',
      buttons: ['preferencesWindow.close.discardChanges', 'generic.cancel'],
    }), true);
    await flushPromises();
    expect(store.state.preferences.changes).toEqual(changes);
    expect(window.close).toHaveBeenCalledTimes(closes);
  });

  it('closes without asking when there are no unsaved changes', async() => {
    const store = mountPreferences();

    await store.dispatch('preferences/clear');
    expect(beforeUnloadCancelled()).toBe(false);
    expect(ipcRenderer.invoke).not.toHaveBeenCalled();
  });

  it('closes without asking after a successful apply', async() => {
    mountPreferences();

    wrapper!.findComponent({ name: 'preferences-footer' }).vm.$emit('apply');
    await flushPromises();
    expect(window.close).toHaveBeenCalled();
    expect(beforeUnloadCancelled()).toBe(false);
    expect(ipcRenderer.invoke).not.toHaveBeenCalled();
  });

  it('switches the language on apply before closing', async() => {
    const store = mountPreferences();
    const switchTo = jest.fn();

    store.commit('preferences/SET_CHANGES', { 'application.locale': 'ko' });
    store.subscribeAction(({ type, payload }) => {
      if (type === 'i18n/switchTo') {
        switchTo(payload, jest.mocked(window.close).mock.calls.length);
      }
    });
    wrapper!.findComponent({ name: 'preferences-footer' }).vm.$emit('apply');
    await flushPromises();
    expect(switchTo).toHaveBeenCalledWith('ko', 0);
    expect(window.close).toHaveBeenCalled();
  });

  it('keeps the language on an apply without a language change', async() => {
    const store = mountPreferences();
    const actions: string[] = [];

    store.commit('preferences/SET_CHANGES', { 'kubernetes.enabled': true });
    store.subscribeAction(({ type }) => { actions.push(type) });
    wrapper!.findComponent({ name: 'preferences-footer' }).vm.$emit('apply');
    await flushPromises();
    expect(actions).not.toContain('i18n/switchTo');
    expect(window.close).toHaveBeenCalled();
  });

  it('keeps the language and the window open when the apply fails', async() => {
    const store = mountPreferences(false);
    const actions: string[] = [];

    store.subscribeAction(({ type }) => { actions.push(type) });
    wrapper!.findComponent({ name: 'preferences-footer' }).vm.$emit('apply');
    await flushPromises();
    expect(actions).not.toContain('i18n/switchTo');
    expect(window.close).not.toHaveBeenCalled();
  });

  it('lets the window close after unmount', () => {
    const store = mountPreferences();

    wrapper!.unmount();
    wrapper = undefined;
    store.commit('preferences/SET_CHANGES', { 'application.locale': 'de' });
    expect(beforeUnloadCancelled()).toBe(false);
  });
});
