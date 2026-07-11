import _ from 'lodash';
import { toRaw } from 'vue';
import { MutationTree, Plugin } from 'vuex';

import type { RootState } from '@pkg/entry/store';
import type { ActionTree, MutationsType } from '@pkg/store/ts-helpers';
import defaultTransientPreferences, { TransientPreferencesState } from '@pkg/types/transientPreferences';
import ipcRenderer from '@pkg/utils/ipcRenderer';
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
 */
type NavigationInput = {
  [K in keyof NavigationInputUnion]: Pick<NavigationInputUnion, K>;
}[keyof NavigationInputUnion];

export const actions = {
  async navigate({ state, commit }, navigation: NavigationInput) {
    commit('navigate', navigation);
    await ipcRenderer.invoke('transient-preferences/set', toRaw(state));
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
