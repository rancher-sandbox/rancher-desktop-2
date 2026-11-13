import { jest } from '@jest/globals';
import { flushPromises, mount } from '@vue/test-utils';
import { createStore } from 'vuex';

import mockModules from '@pkg/utils/testUtils/mockModules';

mockModules({
  '@pkg/entry/store':       {},
  '@pkg/utils/ipcRenderer': { ipcRenderer: { on: jest.fn(), once: jest.fn(), send: jest.fn() } },
});

// jsdom doesn't provide structuredClone, and the `preferences/preferences` getter calls it.
globalThis.structuredClone ??= (value: any) => JSON.parse(JSON.stringify(value));

const i18n = await import('@pkg/store/i18n');
const preferences = await import('@pkg/store/preferences');
const { default: ApplicationGeneral } = await import('@pkg/components/Preferences/ApplicationGeneral.vue');

describe('ApplicationGeneral', () => {
  it('shows a picked language after a tab switch', async() => {
    const store = createStore({
      modules: {
        i18n:             { namespaced: true, ...i18n } as any,
        preferences:      { namespaced: true, ...preferences } as any,
        rdd:              { namespaced: true, getters: { app: () => ({ metadata: {}, spec: { application: { locale: 'en-us' } } }) } },
        'rdd-connection': { namespaced: true, state: () => ({ config: { makeApiClient: () => ({}) } }) },
      },
      plugins: [...i18n.plugins, ...preferences.plugins] as any,
    });
    const options = {
      global: {
        plugins:    [store],
        mocks:      { t: (key: string) => key },
        stubs:      { RdCheckbox: true },
        directives: { cleanTooltip: {}, tooltip: {} },
      },
    };

    await store.dispatch('i18n/switchTo', 'en-us');
    await mount(ApplicationGeneral, options).find('select').setValue('ko');
    await flushPromises();

    // Switching to another Application tab and back mounts the component again.
    const wrapper = mount(ApplicationGeneral, options);

    expect((wrapper.find('select').element as HTMLSelectElement).value).toBe('ko');
  });
});
