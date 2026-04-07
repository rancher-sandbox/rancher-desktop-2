import { Plugin } from 'vuex';

import { defineResource, listNamespacedResource, resourceActionName, resourceMutations, resourceState, resourceWatchActions } from '@pkg/store/rddConnection';
import { ActionContext, ActionTree, GetterTree, MutationsType } from '@pkg/store/ts-helpers';
import * as RDDClient from '@rdd-client';

type ContainerEngineState = ReturnType<typeof state>;

function namespacedResourcePath(typePlural: string) {
  return function(namespace: string) {
    return `/apis/containers.rancherdesktop.io/v1alpha1/namespaces/${ namespace }/${ typePlural }`;
  };
}

function resourceFieldSelector(untypedContext: any): string | undefined {
  const context: ActionContext<ContainerEngineState> = untypedContext;
  const currentNamespace = context.getters.currentNamespace;

  return currentNamespace ? `status.namespace=${ currentNamespace }` : undefined;
}

const resources = [
  defineResource({
    name:          'containers',
    path:          namespacedResourcePath('containers'),
    makeClient:    config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    list:          listNamespacedResource('Container'),
    fieldSelector: resourceFieldSelector,
  }),
  defineResource({
    name:          'images',
    path:          namespacedResourcePath('images'),
    makeClient:    config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    list:          listNamespacedResource('Image'),
    fieldSelector: resourceFieldSelector,
  }),
  defineResource({
    name:          'namespaces',
    type:          'containerNamespace',
    path:          namespacedResourcePath('containernamespaces'),
    makeClient:    config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    list:          listNamespacedResource('ContainerNamespace'),
  }),
  defineResource({
    name:          'volumes',
    path:          namespacedResourcePath('volumes'),
    makeClient:    config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    list:          listNamespacedResource('Volume'),
    fieldSelector: resourceFieldSelector,
  }),
] as const;

type errorSource = 'containers' | 'images' | 'volumes' | 'namespaces';
type resourceKeys = Exclude<keyof ReturnType<typeof resourceState<typeof resources>>, '_watchers'>;

export const state = () => ({
  ...resourceState(resources),
  currentNamespace: undefined as string | undefined,
  error:            undefined as undefined | { error: Error, source: errorSource },
});

export const getters = {
  supportsNamespaces(): boolean {
    // TODO: Determine if the backend supports namespaces.
    return false;
  },
  currentNamespace(state, getters): string | undefined {
    return getters.supportsNamespaces ? state.currentNamespace : undefined;
  },
  containerById(state) {
    return (id: string) => state.containers?.find(container => container.metadata?.name === id);
  },
} satisfies GetterTree<ContainerEngineState>;

export const mutations = {
  ...resourceMutations(resources),
  SET_CURRENT_NAMESPACE(state, namespace) {
    state.currentNamespace = namespace;
  },
  SET_ERROR(state, payload?) {
    state.error = payload;
  },
} satisfies MutationsType<ContainerEngineState>;

export const actions = {
  ...resourceWatchActions(resources),
  setCurrentNamespace({ commit, getters, state, dispatch }, { namespace }: { namespace: string | undefined }) {
    if (namespace === state.currentNamespace) {
      return;
    }
    if (!getters.supportsNamespaces) {
      const error = new Error('Current container engine does not support namespaces');
      commit('SET_ERROR', { error, source: 'namespaces' });
      console.log(error);
    } else if (namespace !== undefined && !state.namespaces?.some(ns => ns.metadata?.name === namespace)) {
      const error = new Error(`Cannot set current namespace to nonexistent namespace ${ namespace }`);
      commit('SET_ERROR', { error, source: 'namespaces' });
      console.log(error);
    } else {
      commit('SET_CURRENT_NAMESPACE', namespace);
      // Refresh all resources to update the namespace filter.
      for (const key of Object.keys(state._watchers)) {
        dispatch(`watch${ key.replace(/^\w/, c => c.toUpperCase()) }`,
          state._watchers[key as resourceKeys].options);
      }
    }
  },
  /** Request the given container to transition to the provided state. */
  containerSetState(
    { rootState, commit },
    { container, state }: {
      container: RDDClient.IoRancherdesktopContainersV1alpha1Container,
      state:     'running' | 'stopped',
    },
  ) {
    const config: RDDClient.KubeConfig = rootState['rdd-connection'].config;
    const client = config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    return client.patchNamespacedContainer(
      {
        name:            container.metadata!.name!,
        namespace:       container.metadata!.namespace!,
        body:            { spec: { state } },
        fieldValidation: 'Strict',
      }).catch((err: Error) => {
      commit('SET_ERROR', { error: err, source: 'containers' });
    });
  },
  /** Delete the given container. */
  async containerDelete(
    { rootState, commit },
    { container }: {
      container: RDDClient.IoRancherdesktopContainersV1alpha1Container,
    },
  ) {
    const config: RDDClient.KubeConfig = rootState['rdd-connection'].config;
    const client = config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    try {
      const status = await client.deleteNamespacedContainer({
        name:      container.metadata!.name!,
        namespace: container.metadata!.namespace!,
      });

      if (status.status !== 'Success') {
        commit('SET_ERROR', {
          error:  new Error(`Failed to delete container ${ container.metadata!.name }: ${ status.message }`),
          source: 'containers',
        });
      }
    } catch (error: any) {
      commit('SET_ERROR', { error, source: 'containers' });
    }
  },
  imagePush(
    { rootState },
    { image }: {
      image: RDDClient.IoRancherdesktopContainersV1alpha1Image,
    },
  ) {
    const config: RDDClient.KubeConfig = rootState['rdd-connection'].config;
    const client = config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    return client.createNamespacedImagePushRequest({
      namespace: image.metadata!.namespace!,
      body:      {
        metadata: {
          namespace:    image.metadata!.namespace,
          generateName: `image-push-${ image.metadata!.name! }-`,
        },
        spec: {
          imageRef: image.metadata!.name!,
        },
      },
    });
  },
  imageScan(
    { rootState },
    { image }: {
      image: RDDClient.IoRancherdesktopContainersV1alpha1Image,
    },
  ) {
    const config: RDDClient.KubeConfig = rootState['rdd-connection'].config;
    const client = config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    return client.createNamespacedImageScanRequest({
      namespace: image.metadata!.namespace!,
      body:      {
        metadata: {
          namespace:    image.metadata!.namespace,
          generateName: `image-scan-${ image.metadata!.name! }-`,
        },
        spec: {
          imageRef: image.metadata!.name!,
        },
      },
    });
  },
  async imageDelete(
    { rootState, commit },
    { image }: {
      image: RDDClient.IoRancherdesktopContainersV1alpha1Image,
    },
  ) {
    const config: RDDClient.KubeConfig = rootState['rdd-connection'].config;
    const client = config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    try {
      const status = await client.deleteNamespacedImage({
        name:      image.metadata!.name!,
        namespace: image.metadata!.namespace!,
      });
      if (status.status !== 'Success') {
        commit('SET_ERROR', {
          error:  new Error(`Failed to delete image ${ image.metadata!.name }: ${ status.message }`),
          source: 'images',
        });
      }
    } catch (error: any) {
      commit('SET_ERROR', { error, source: 'images' });
    }
  },
  async volumeDelete(
    { rootState, commit },
    { volume }: {
      volume: RDDClient.IoRancherdesktopContainersV1alpha1Volume,
    },
  ) {
    const config: RDDClient.KubeConfig = rootState['rdd-connection'].config;
    const client = config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    try {
      const status = await client.deleteNamespacedVolume({
        name:      volume.metadata!.name!,
        namespace: volume.metadata!.namespace!,
      });
      if (status.status !== 'Success') {
        commit('SET_ERROR', {
          error:  new Error(`Failed to delete volume ${ volume.metadata!.name }: ${ status.message }`),
          source: 'volumes',
        });
      }
    } catch (error: any) {
      commit('SET_ERROR', { error, source: 'volumes' });
    }
  },
} satisfies ActionTree<ContainerEngineState, any, typeof mutations>;

export const plugins: Plugin<ContainerEngineState>[] = [
  function(store) {
    for (const resource of resources) {
      const methodName = resourceActionName('container-engine/setupWatch', resource.name);

      store.dispatch(methodName, {
        callback: (error: Error) => {
          store.commit('SET_ERROR', { error, source: resource.name });
        },
      }).catch((error: Error) => {
        store.commit('SET_ERROR', { error, source: resource.name });
      });
    }
  },
];
