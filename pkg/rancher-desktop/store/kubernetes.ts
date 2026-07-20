import { Plugin } from 'vuex';

import { RootState } from '@pkg/entry/store';
import { defineResource, resourceMutations, resourceState, resourceWatchActions } from '@pkg/store/rddConnection';
import type { ActionTree, GetterTree, MutationsType } from '@pkg/store/ts-helpers';
import * as RDDClient from '@rdd-client';

type KubernetesState = ReturnType<typeof state>;

const resources = [
  defineResource({
    name:       'configMaps',
    type:       'ConfigMap',
    path:       (namespace: string) => `/api/v1/namespaces/${ namespace }/configmaps`,
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
  }),
] as const;

export const state = () => ({
  ...resourceState(resources),
  error: undefined as Error | undefined,
});

/**
 * Parse the input as JSON, returning undefined if that fails.
 */
function tryParseJSON<T>(input: string | undefined): T | undefined {
  if (input === undefined) {
    return undefined;
  }
  try {
    return JSON.parse(input);
  } catch {
    return undefined;
  }
}

export const getters = {
  k3sVersions(state) {
    const configMap = state.configMaps?.find(cm => cm.metadata?.name === 'k3s-versions');
    const versions = configMap?.data?.versions;

    return tryParseJSON<Record<string, string>>(versions);
  },
  k3sChannels(state) {
    const configMap = state.configMaps?.find(cm => cm.metadata?.name === 'k3s-versions');
    const channels = configMap?.data?.channels;

    return tryParseJSON<Record<string, string>>(channels);
  },
} satisfies GetterTree<KubernetesState>;

export const mutations = {
  ...resourceMutations(resources),
  SET_ERROR(state, error: Error | undefined) {
    state.error = error;
  },
} satisfies MutationsType<KubernetesState>;

export const actions = {
  ...resourceWatchActions('kubernetes', resources),
} satisfies ActionTree<KubernetesState>;

export const plugins: Plugin<RootState>[] = [
  // Start watching resources immediately.
  function(store) {
    store.dispatch('kubernetes/setupResourceWatch', {
      callback: (error: Error) => {
        console.error('Error watching Kubernetes resources:', error);
        store.commit('kubernetes/SET_ERROR', error);
      },
    }).catch((error: Error) => {
      console.error('Failed to set up watch for Kubernetes resources:', error);
      store.commit('kubernetes/SET_ERROR', error);
    });
  },
];
