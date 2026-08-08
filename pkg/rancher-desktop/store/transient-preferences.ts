import _ from 'lodash';
import { toRaw } from 'vue';
import { MutationTree, Plugin } from 'vuex';

import type { RootState } from '@pkg/entry/store';
import type { ActionTree, MutationsType } from '@pkg/store/ts-helpers';
import defaultTransientPreferences, { TransientPreferencesState } from '@pkg/types/transientPreferences';
import ipcRenderer from '@pkg/utils/ipcRenderer';
import { kebabCase } from '@pkg/utils/string-utils';
import { FieldType, RecursiveLeafKeys, RecursiveReadonly } from '@pkg/utils/typeUtils';

export const state = () => { return structuredClone(defaultTransientPreferences) };

export const mutations = {
  SET_ALL(state, preferences: Partial<RecursiveReadonly<TransientPreferencesState>>) {
    _.merge(state, preferences);
  },
  /**
   * @note This should not be used directly; use the `navigate` action instead.
   */
  navigate(state, navigation: NavigationInput) {
    for (const key of Object.keys(navigation)) {
      _.set(state.navigation, key, navigation[key as keyof NavigationInput]);
    }
  },
} satisfies MutationsType<TransientPreferencesState> & MutationTree<TransientPreferencesState>;

// Implementation detail of NavigationInput.
type NavigationInputUnion = {
  [K in RecursiveLeafKeys<TransientPreferencesState['navigation']>]: FieldType<TransientPreferencesState['navigation'], K>;
};
/**
 * NavigationInput is a type that represents the input to the navigate action.
 * It must be a record with a single key, which must be a dot-separated key of
 * the navigation state, and the value must be the new value for that key.
 * Optionally, any key may be paired with `preferences.top` to move directly to
 * a specific tab.
 */
type NavigationInput = {
  [K in keyof NavigationInputUnion]:
  K extends 'preferences.top'
    ? { 'preferences.top': NavigationInputUnion['preferences.top'] }
    : Pick<NavigationInputUnion, K> & {
      'preferences.top'?: K extends `preferences.${ infer TabName }`
        ? TabName
        : NavigationInputUnion['preferences.top'];
    };
}[keyof NavigationInputUnion];

export const actions = {
  async navigate({ state, commit }, navigation: NavigationInput) {
    commit('navigate', navigation);
    await ipcRenderer.invoke('transient-preferences/set', toRaw(state));
  },
  /**
   * Click handler for data-navigate attributes in translated HTML strings.
   * Parses "page,tab" from the attribute and calls the navigate function.
   */
  async navigateByClick({ dispatch }, event: MouseEvent) {
    const target: HTMLElement | null = (event.target as HTMLElement)?.closest('[data-navigate]');
    const nav = target?.dataset.navigate;

    if (nav) {
      // data-navigate values from translations are not validated against known
      // page names; malformed values produce a no-op navigation.
      const [navItem, tab] = nav.split(',').map(s => kebabCase(s.trim()));
      const args = Object.fromEntries([
        ['preferences.top', navItem],
        [`preferences.${ navItem }`, tab],
      ].filter(([_, value]) => !!value)) as NavigationInput;
      await dispatch('navigate', args);
    }
  },
} satisfies ActionTree<TransientPreferencesState>;

export const plugins: Plugin<RootState>[] = [
  // Load the state from the main process.
  function(store) {
    ipcRenderer.on('transient-preferences/update', (_event, preferences) => {
      store.commit('transient-preferences/SET_ALL', preferences);
    });
    ipcRenderer.send('transient-preferences/get');
  },
];
