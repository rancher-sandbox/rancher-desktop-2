import { RootState } from '@pkg/entry/store';
import { defineResource, resourceMutations, resourceState, resourceWatchActions, ResourceNames } from '@pkg/store/rddConnection';
import { ActionContext, ActionTree, GetterTree, MutationsType } from '@pkg/store/ts-helpers';
import { defined, RecursivePartial } from '@pkg/utils/typeUtils';
import * as RDDClient from '@rdd-client';

import type { Plugin, Store } from 'vuex';

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

/**
 * Whether a delete failed because the resource no longer exists. The user
 * wanted it gone, so the delete actions do not report this as an error.
 */
function isNotFound(error: unknown): error is RDDClient.ApiException<unknown> {
  return error instanceof RDDClient.ApiException && error.code === 404;
}

const resources = [
  defineResource({
    name:          'containers',
    path:          namespacedResourcePath('containers'),
    makeClient:    config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    fieldSelector: resourceFieldSelector,
  }),
  defineResource({
    name:          'images',
    path:          namespacedResourcePath('images'),
    makeClient:    config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    fieldSelector: resourceFieldSelector,
  }),
  defineResource({
    name:          'namespaces',
    type:          'ContainerNamespace',
    path:          namespacedResourcePath('containernamespaces'),
    makeClient:    config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
  }),
  defineResource({
    name:          'volumes',
    path:          namespacedResourcePath('volumes'),
    makeClient:    config => config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
    fieldSelector: resourceFieldSelector,
  }),
] as const;

type resourceNames = ResourceNames<typeof resources>;

export const state = () => ({
  ...resourceState(resources),
  /**
   * The currently selected namespace; {@link getters.currentNamespace} should
   * be used instead to look up the active namespace, and
   * {@link actions.setCurrentNamespace} should be used to change the value.
   */
  selectedNamespace: undefined as string | undefined,
  error:             undefined as undefined | { error: Error, source: resourceNames },
});

export const getters = {
  /** Whether the current container engine supports namespaces */
  supportsNamespaces(state, getters, rootState, rootGetters): boolean {
    const app: RDDClient.IoRancherdesktopAppV1alpha1App | undefined = rootGetters['rdd/app'];

    return app?.status?.supportsNamespaces ?? false;
  },
  /** The namespaces in the current container engine, as an array of names */
  namespaces(state): string[] {
    return (state.namespaces ?? []).map(ns => ns.status?.name).filter(defined);
  },
  /** The currently selected namespace, or undefined if namespaces are not supported */
  currentNamespace(state, getters): string | undefined {
    if (!getters.supportsNamespaces) {
      return undefined;
    }
    if (state.selectedNamespace && getters.namespaces.includes(state.selectedNamespace)) {
      return state.selectedNamespace;
    }
    return getters.namespaces.includes('default') ? 'default' : getters.namespaces[0];
  },
  /** Get a container by its ID */
  containerById(state) {
    return (id: string) => state.containers?.find(container => container.metadata?.name === id);
  },
} satisfies GetterTree<ContainerEngineState>;

export const mutations = {
  ...resourceMutations(resources),
  SET_SELECTED_NAMESPACE(state, namespace) {
    state.selectedNamespace = namespace;
  },
  SET_ERROR(state, payload?) {
    state.error = payload;
  },
} satisfies MutationsType<ContainerEngineState>;

export const actions = {
  ...resourceWatchActions('container-engine', resources),
  setCurrentNamespace({ commit, getters, state }, { namespace }: { namespace: string | undefined }) {
    if (namespace === state.selectedNamespace) {
      return;
    }
    if (!getters.supportsNamespaces) {
      const error = new Error('Current container engine does not support namespaces');
      commit('SET_ERROR', { error, source: 'namespaces' });
      console.log(error);
    } else if (namespace !== undefined && !getters.namespaces.includes(namespace)) {
      const error = new Error(`Cannot set current namespace to nonexistent namespace ${ namespace }`);
      commit('SET_ERROR', { error, source: 'namespaces' });
      console.log(error);
    } else {
      commit('SET_SELECTED_NAMESPACE', namespace);
      // The Vuex plugin below will cause the resource watches to be refreshed
      // when the `currentNamespace` getter returns a different result.
    }
  },
  /** Request the given container to transition to the provided state. */
  containerRequestAction(
    { rootState, commit },
    { container, state }: {
      container: RDDClient.IoRancherdesktopContainersV1alpha1Container,
      state:     'start' | 'stop' | 'pause' | 'unpause' | 'restart',
    },
  ) {
    const config: RDDClient.KubeConfig = rootState['rdd-connection'].config;
    const client = config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);
    const body: RecursivePartial<RDDClient.IoRancherdesktopContainersV1alpha1Container> = {
      metadata: {
        annotations: {
          'containers.rancherdesktop.io/action': state,
        },
      },
    };

    return client.patchNamespacedContainer(
      {
        name:            container.metadata!.name!,
        namespace:       container.metadata!.namespace!,
        body,
        fieldValidation: 'Strict',
      },
      RDDClient.setHeaderOptions('Content-Type', RDDClient.PatchStrategy.MergePatch),
    ).catch((err: Error) => {
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
    const { config } = rootState['rdd-connection'];

    try {
      await deleteResource(
        config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
        container,
        container.status?.name ?? container.metadata!.name!,
        'deleteNamespacedContainer',
        'Container');
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
    const { config } = rootState['rdd-connection'];

    try {
      await deleteResource(
        config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
        image,
        image.status?.repoTag ?? image.metadata!.name!,
        'deleteNamespacedImage',
        'Image');
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
    const { config } = rootState['rdd-connection'];

    try {
      await deleteResource(
        config.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api),
        volume,
        volume.status?.name ?? volume.metadata!.name!,
        'deleteNamespacedVolume',
        'Volume');
    } catch (error: any) {
      commit('SET_ERROR', { error, source: 'volumes' });
    }
  },
} satisfies ActionTree<ContainerEngineState, any, typeof mutations>;

/**
 * Deletes a resource from the container engine.
 *
 * @param client The API client to use for the deletion.
 * @param obj The object to delete.
 * @param name The name of the object, for display purposes.
 * @param method The method to call on the API client to delete the resource.
 * @param kind The kind of the resource.
 * @throws If the resource failed to delete.
 */
async function deleteResource<
  M extends Exclude<
    keyof RDDClient.ContainersRancherdesktopIoV1alpha1Api & `delete${ string }`,
    `${ string }WithHttpInfo`>,
>(
  client: RDDClient.ContainersRancherdesktopIoV1alpha1Api,
  obj: RDDClient.KubernetesObject,
  name: string,
  method: M,
  kind: string,
) {
  const result = await (async() => {
    try {
      return await client[method]({
        name:      obj.metadata!.name!,
        namespace: obj.metadata!.namespace!,
      }) as RDDClient.V1Status | RDDClient.KubernetesObject;
    } catch (ex) {
      if (isNotFound(ex)) {
        return ex;
      }
      throw ex;
    }
  })();
  if (isNotFound(result)) {
    return;
  }
  if (result.kind === 'Status' || result.kind === undefined) {
    const status: RDDClient.V1Status = result;
    if (status.status !== 'Success') {
      throw new Error(`Failed to delete ${ kind } ${ name }: ${ status.message }`);
    }
    return;
  }
  if (result.kind === kind) {
    // This can happen if the object has a finalizer; instead of committing the
    // delete and returning a status, it's pending instead and the full object
    // is returned instead.  The @kubernetes/client-node package does not return
    // the correct object in this case due to OpenAPI v2 limitations; therefore,
    // we assume that delete succeeded.
    // @see https://github.com/kubernetes-client/javascript/issues/263
    return;
  }
  throw new Error(`Failed to delete ${ kind } ${ name }: got unexpected response kind ${ result.kind }`);
}

export const plugins: Plugin<RootState>[] = [
  // Start watching resources immediately.
  function(store) {
    store.dispatch('container-engine/setupResourceWatch', {
      callback: (error: Error, source) => {
        console.error('Error watching container engine resources:', error);
        store.commit('container-engine/SET_ERROR', { error, source });
      },
    }).catch((error: Error) => {
      console.error('Failed to set up watch for container engine resources:', error);
      // There is no specific resource associated with this error.
      store.commit('container-engine/SET_ERROR', { error, source: 'containers' });
    });
  },
  // Force re-watching resources when the current namespace changes.
  function(store: Store<RootState>) {
    const namespacedResourceNames = resources
      .filter(r => r.fieldSelector === resourceFieldSelector)
      .map(r => r.name);
    store.watch(
      (_state, getters) => getters['container-engine/currentNamespace'],
      async() => {
        try {
          await store.dispatch('container-engine/rewatchResources', namespacedResourceNames);
        } catch (error) {
          store.commit('container-engine/SET_ERROR', { error: error as Error, source: 'containers' });
          console.error('Error setting up resource watch on namespace change:', error);
        }
      },
    );
  },
];
