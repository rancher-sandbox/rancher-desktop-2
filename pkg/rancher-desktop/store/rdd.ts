import { Plugin } from 'vuex';

import type { RootState } from '@pkg/entry/store';
import { defineResource, listNamespacedResource, resourceMutations, resourceState, resourceWatchActions } from '@pkg/store/rddConnection';
import { ActionTree, GetterTree, MutationsType } from '@pkg/store/ts-helpers';
import * as RDDClient from '@rdd-client';

type RDDState = ReturnType<typeof state>;

const resources = [
  defineResource({
    name:       'namespaces',
    path:       () => '/api/v1/namespaces',
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
    list:       client => client.listNamespace(),
  }),
  defineResource({
    name:       'configMaps',
    path:       (namespace) => `/api/v1/namespaces/${ namespace }/configmaps`,
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
    list:       listNamespacedResource('ConfigMap'),
  }),
  defineResource({
    name:       'systemConfigMaps',
    type:       'ConfigMap',
    path:       () => '/api/v1/namespaces/rdd-system/configmaps',
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
    list:       client => client.listNamespacedConfigMap({ namespace: 'rdd-system' }),
  }),
  defineResource({
    name:       'apps',
    path:       () => '/apis/app.rancherdesktop.io/v1alpha1/apps',
    makeClient: config => config.makeApiClient(RDDClient.AppRancherdesktopIoV1alpha1Api),
    list:       client => client.listApp(),
  }),
] as const;

export const state = () => ({
  ...resourceState(resources),
  error: undefined as Error | undefined,
});

export const getters = {
  app(state) {
    // App is a singleton, so just return the first one.
    return state.apps?.find(app => !!app);
  },
  status(state, getters) {
    return function(type: string) {
      const conditions: any[] | undefined = getters.app?.status?.conditions;
      return conditions?.find((condition: any) => condition.type === type)?.status === 'True';
    };
  },
  settled(state, getters) {
    return getters.status('Settled');
  },
} satisfies GetterTree<RDDState>;

export const mutations = {
  ...resourceMutations(resources),
  SET_ERROR(state, error: Error | undefined) {
    state.error = error;
  },
} satisfies MutationsType<RDDState>;

export const actions = {
  ...resourceWatchActions('rdd', resources),

  /** Ensure that the application is started. */
  async ensureAppStarted({ dispatch, getters, rootState, commit }) {
    for (let attempt = 0; true; attempt++) {
      try {
        await dispatch('waitForResources', ['apps']);
        commit('SET_ERROR', undefined);
        break;
      } catch (error) {
        console.error('Error waiting for app resource:', error);
        commit('SET_ERROR', error as Error);
        // Quick exponential backoff; we will loop forever until success.
        const delay = Math.min(2 ** attempt * 1_000, 30_000);
        await new Promise(resolve => setTimeout(resolve, delay));
      }
    }

    const { config } = rootState['rdd-connection'];
    const client = config.makeApiClient(RDDClient.AppRancherdesktopIoV1alpha1Api);

    let app = getters.app;
    while (!app?.metadata?.name) {
      try {
        await client.createApp({
          body: {
            kind:       'App',
            apiVersion: 'app.rancherdesktop.io/v1alpha1',
            metadata:   {
              name: 'app',
            },
            spec: {
              namespace: 'rancher-desktop',
              running:   false,
            },
          },
        });
      } catch (err) {
        if (err instanceof Error && 'code' in err && err.code === 409) {
          // HTTP 409 Conflict means the app already exists; that would mean we
          // hit a TOCTTOU race, at which point we can ignore it and just set it
          // to running.
        } else {
          console.error('Failed to create app, will retry:', err);
          commit('SET_ERROR', err as Error);
          return;
        }
      }
      // We need a slight pause to ensure that the watch has managed to pick
      // up the new app.
      await new Promise(resolve => setTimeout(resolve, 100));
      app = getters.app;
    }

    // Start the app.
    await client.patchApp(
      { name: app.metadata.name, body: { spec: { running: true } } },
      RDDClient.setHeaderOptions('Content-Type', RDDClient.PatchStrategy.MergePatch),
    ).catch((err: Error) => {
      console.error(err);
      commit('SET_ERROR', err);
    });
  },
} satisfies ActionTree<RDDState, RootState, typeof mutations, typeof getters>;

export const plugins: Plugin<RDDState>[] = [
  function(store) {
    store.dispatch('rdd/setupResourceWatch', {
      callback: (error: Error) => {
        console.error('Error watching RDD resources:', error);
        store.commit('rdd/SET_ERROR', error);
      },
    }).catch((error: Error) => {
      console.error('Failed to set up watch for RDD resources:', error);
      store.commit('rdd/SET_ERROR', error);
    });
  },
];
