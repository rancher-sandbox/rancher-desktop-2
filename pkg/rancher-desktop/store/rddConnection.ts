// This file contains the state required to maintain a connection to the Rancher
// Desktop Daemon.

import { markRaw } from 'vue';
import { Plugin } from 'vuex';

import { ActionContext, ActionTree, Commit, MutationsType } from '@pkg/store/ts-helpers';
import ipcRenderer from '@pkg/utils/ipcRenderer';
import Latch from '@pkg/utils/latch';
import { UpperSnakeCase } from '@pkg/utils/typeUtils';
import * as RDDClient from '@rdd-client';

/**
 * RDDState describes the connection to the Rancher Desktop Daemon.
 */
export interface RDDConnectionState {
  /** config is the Kubernetes configuration to use. */
  config:              RDDClient.KubeConfig;
  error:               any;
  watch:               RDDClient.Watch;
  /** The key should be of the form `module/resource`. */
  disconnectCallbacks: Record<string, () => void>;
  /** Kubernetes namespace RDD objects live in; must not be empty. */
  namespace:           string;
};

/** Vuex state for managing the RDD connection. */
export const state: () => RDDConnectionState = () => {
  const config = new RDDClient.KubeConfig();
  return {
    config,
    error:               undefined,
    watch:               markRaw(new RDDClient.Watch(config)),
    disconnectCallbacks: {},
    namespace:           'rancher-desktop',
  };
};

/** Vuex mutations for managing the RDD connection. */
export const mutations = {
  SET_CONFIG(state, config) {
    state.config = config;
    state.watch.config = config;
  },
  SET_ERROR(state, error) {
    state.error = error ? markRaw(error) : error;
  },
  SET_DISCONNECT_CALLBACKS(state, callbacks) {
    state.disconnectCallbacks = callbacks;
  },
} satisfies MutationsType<RDDConnectionState>;

/**
 * fetchConfigPromise is used to ensure that multiple concurrent calls to
 * `fetchConfig` only runs once; this is so that we do not trigger multiple
 * `notifyDisconnected` calls on disconnect.
 */
let fetchConfigPromise: Promise<RDDClient.KubeConfig> | undefined;

/** Vuex actions for managing the RDD connection. */
export const actions = {
  /**
   * Fetch the Kubernetes configuration from the backend and update the state.
   * This may be called multiple times concurrently.
   */
  async fetchConfig({ commit, dispatch }) {
    if (!fetchConfigPromise) {
      fetchConfigPromise = (async() => {
        try {
          const config = await ipcRenderer.invoke('rdd/kube-config');
          const kubeConfig = new RDDClient.KubeConfig();

          kubeConfig.loadFromString(config);
          commit('SET_CONFIG', kubeConfig);
          await dispatch('notifyDisconnected');
          commit('SET_ERROR', undefined);

          return kubeConfig;
        } catch (ex) {
          commit('SET_ERROR', ex);
          throw ex;
        } finally {
          fetchConfigPromise = undefined;
        }
      })();
    }
    return fetchConfigPromise;
  },
  registerDisconnectCallback({ state, commit }, { key, callback }: { key: string, callback?: () => void }) {
    const callbacks = { ...state.disconnectCallbacks };
    if (!callback) {
      delete callbacks[key];
    } else {
      callbacks[key] = callback;
    }
    commit('SET_DISCONNECT_CALLBACKS', callbacks);
  },
  notifyDisconnected({ commit, state }) {
    for (const [key, callback] of Object.entries(state.disconnectCallbacks)) {
      try {
        callback();
      } catch (err) {
        console.error(`Error in RDD disconnect callback ${ key }:`, err);
      }
    }
    commit('SET_DISCONNECT_CALLBACKS', {});
  },
} satisfies ActionTree<RDDConnectionState, any, typeof mutations>;

/** Vuex plugins for managing the RDD connection. */
export const plugins: Plugin<RDDConnectionState>[] = [
  function(store) {
    store.dispatch('rdd-connection/fetchConfig').catch(ex => {
      console.error(ex);
    });
  },
];

/// /////////////////////////////////////////////////////////////////////////////
// The rest of the file is helpers to implement stores for Kubernetes resources.
/// /////////////////////////////////////////////////////////////////////////////

/**
 * ListFunctionType describes the type of a list function in the RDD client.
 */
type ListFunctionType<T extends RDDClient.KubernetesObject> =
  (param?: any, options?: RDDClient.ConfigurationOptions) => Promise<RDDClient.KubernetesListObject<T>>;

/**
 * ItemType extracts the item type from the list function in the client.
 */
type ItemType<C, TypeName extends string> =
  C extends Record<`list${ Capitalize<TypeName> }`, ListFunctionType<infer T>> ? T
    : C extends Record<`listNamespaced${ Capitalize<TypeName> }`, ListFunctionType<infer T>> ? T
      : never;

/**
 * Converts a union type to an intersection type.
 * `A | B | C` becomes `A & B & C`.
 */
type UnionToIntersection<U> =
  (U extends any ? (k: U) => void : never) extends ((k: infer I) => void) ? I : never;

/**
 * Intersects all elements of a mapped type indexed by number.
 * `{ 0: A, 1: B, 2: C }` becomes `A & B & C`.
 */
type IntersectMapped<T> =
  UnionToIntersection<T[keyof T & number]>;

/**
 * ResourceTypeLike is a loose structural type used for parameter constraints.
 * It avoids invariance issues that arise from using ResourceType directly.
 */
interface ResourceTypeLike {
  name:           string;
  type?:          string;
  path:           (namespace: string) => string;
  labelSelector?: (context: any) => string | undefined;
  fieldSelector?: (context: any) => string | undefined;
  makeClient:     (config: RDDClient.KubeConfig) => any;
  list:           (client: any, options: ListResourceOptions<any>) => Promise<RDDClient.KubernetesListObject<any>>;
}

/**
 * ListResourceOptions describes the options parameter for a ResourceType's
 * `list` method.
 */
export interface ListResourceOptions<State> {
  /** Kubernetes namespace the objects live in; must not be empty. */
  namespace:       string,
  /** The associated state object. */
  state:           State,
  connectionState: RDDConnectionState,
  /** Any label selectors */
  labelSelector?:  string,
  /** Any field selectors */
  fieldSelector?:  string,
}

/**
 * ResourceType describes a resource type definition.
 */
interface ResourceType<C, StateName extends string, TypeName extends string> extends ResourceTypeLike {
  /** The name of the state, typically plural. */
  name:           StateName;
  /** The name of the resource kind, used in the API name. */
  type?:          TypeName;
  /**
   * The API path to the resources, given the Kubernetes namespace.  The
   * namespace is required.
   */
  path:           (namespace: string) => string;
  /** Optional label selector to filter resources. */
  labelSelector?: (context: any) => string | undefined;
  /** Optional field selector to filter resources. */
  fieldSelector?: (context: any) => string | undefined;
  /** A function which returns the type of client needed to list the resource. */
  makeClient:     (config: RDDClient.KubeConfig) => C,
  /** A function which lists the resource. */
  list:           (client: C, options: ListResourceOptions<ResourceStateItem<StateName, ItemType<C, TypeName>>>) => Promise<RDDClient.KubernetesListObject<ItemType<C, TypeName>>>;
}

/**
 * defineResource is a helper to type a resource definition; it returns its input
 * unchanged.
 */
export function defineResource<
  C,
  StateName extends string,
  TypeName extends string = StateName extends `${ infer S }s` ? S : never,
>(input: ResourceType<C, StateName, TypeName>):
ItemType<C, TypeName> extends never
  ? `${ StateName } has an invalid type`
  : ResourceType<C, StateName, TypeName> {
  return input as any;
};

/**
 * listNamespacedResource is a helper to implement the `list` function in a
 * `ResourceType<...>` when the resource is namespaced.
 * @param typeName the name of the type, in (upper or lower) camel case.
 * @returns A function implementing `ResourceType<...>.list`.
 */
export function listNamespacedResource<
  TypeName extends string,
  C extends Record<
    `listNamespaced${ TypeName }` | `list${ TypeName }ForAllNamespaces`,
    (param?: any, options?: RDDClient.ConfigurationOptions) => any
  >,
>(typeName: TypeName)
  : (client: C, options: ListResourceOptions<any>) => ReturnType<C[`listNamespaced${ TypeName }`]> {
  return (client: C, options: ListResourceOptions<any>) => {
    return client[`listNamespaced${ typeName }`]({
      namespace:     options.namespace,
      fieldSelector: options.fieldSelector,
      labelSelector: options.labelSelector,
    });
  };
}

/**
 * ResourceNames extracts the union of resource names from an array of ResourceTypeLike.
 */
export type ResourceNames<T extends readonly ResourceTypeLike[]> = {
  [K in keyof T]: T[K] extends
  ResourceType<infer C, infer StateName extends string, infer TypeName extends string>
    ? StateName
    : never;
}[number];

/**
 * ResourceStateWatcher defines the type of the _watchers item in the state object.
 */
type ResourceStateWatcher<N extends string, T extends RDDClient.KubernetesObject> =
  Record<N, {
    watcher:  Watcher<N, T>,
    refCount: number,
    options:  ResourceWatchActionsOptions<any> | undefined,
  }>;
/**
 * ResourceStateItem defines the state object derived from one specific resource type.
 */
type ResourceStateItem<Key extends string, T extends RDDClient.KubernetesObject> =
  Record<Key, null | T[]> & { _watchers: ResourceStateWatcher<Key, T>; _watchersInitialized: ReturnType<typeof Latch<void>> };

type ResourceStateReturn<R> =
  R extends ResourceType<infer C, infer StateName extends string, infer TypeName extends string>
    ? ResourceStateItem<StateName, ItemType<C, TypeName>>
    : never;

/**
 * resourceState is a helper function to define the state interface.
 * @param resources Array of literals that satisfy ResourceType<T>.
 * @returns The Vuex state object for the given resources.
 */
export function resourceState<const T extends readonly ResourceTypeLike[]>(resources: T):
IntersectMapped<{ [K in keyof T]: ResourceStateReturn<T[K]> }> {
  return {
    _watchers:            {},
    _watchersInitialized: markRaw(Latch()),
    ...Object.fromEntries(resources.map(r => [r.name, null])),
  } as ReturnType<typeof resourceState<T>>;
}

type ResourceMutationsReturn<R> =
  R extends ResourceType<infer C, infer N extends string, infer TypeName extends string>
    ? {
      [key in N as `SET_${ UpperSnakeCase<key> }`]:
      (state: ResourceStateItem<N, ItemType<C, TypeName>>, payload: ItemType<C, TypeName>[] | null) => void
    } & {
      SET__WATCHERS: ResourceMutationsBuiltin<C, N, TypeName, '_watchers'>,
    }
    : never;
type ResourceMutationsBuiltin<C, N extends string, TypeName extends string, prop extends keyof ResourceStateItem<N, ItemType<C, TypeName>>> =
  (state: ResourceStateItem<N, ItemType<C, TypeName>>, payload: ResourceStateItem<N, ItemType<C, TypeName>>[prop]) => void;

/**
 * resourceMutations is a helper function to define the mutations object.
 * @param resources Array of literals that satisfy ResourceType<T>.
 */
export function resourceMutations<const T extends readonly ResourceTypeLike[]>(resources: T):
IntersectMapped<{ [K in keyof T]: ResourceMutationsReturn<T[K]> }> {
  return {
    SET__WATCHERS: (state: any, payload: any) => {
      state._watchers = markRaw(payload);
    },
    ...Object.fromEntries(resources.map(r => {
      return [`SET_${ UpperSnakeCase(r.name) }`, (state: any, payload: any) => {
        state[r.name] = payload;
      }];
    })),
  } as ReturnType<typeof resourceMutations<T>>;
}

/**
 * ResourceWatchActionsOptions describes options the caller may pass to start
 * watching a resource.
 */
interface ResourceWatchActionsOptions<T extends readonly ResourceTypeLike[]> {
  /** Callback that is invoked when an error occurs. */
  callback?: (error: Error, resourceName: ResourceNames<T>) => void,
}

/** ResourceWatchActionsReturn defines the return type of resourceWatchActions(). */
interface ResourceWatchActionsReturn<T extends readonly ResourceTypeLike[]> {
  /**
   * Set up watching resources; this is expected to be called in a plugin when
   * the store is initialized.  This does not automatically start watching;
   * calling the `watchResources` action is required to start the watch.
   * @param actionContext
   * @param callback
   */
  setupResourceWatch(actionContext: ActionContext<ResourceStateReturn<T[number]>>, options?: ResourceWatchActionsOptions<T>): Promise<void>;

  /**
   * Start watching the specified resources.  This is expected to be called in a
   * component's onBeforeMount() hook.  The promise is resolved once the watch
   * has started, possibly before the initial list has returned; use
   * `waitForResources` to wait for the initial list to be loaded.
   */
  watchResources(actionContext: ActionContext<ResourceStateReturn<T[number]>>, resources: readonly ResourceNames<T>[]): Promise<void>;

  /**
   * Stop watching the specified resources.  This is expected to be called in a
   * component's onBeforeUnmount() hook.
   */
  unwatchResources(actionContext: ActionContext<ResourceStateReturn<T[number]>>, resources: readonly ResourceNames<T>[]): Promise<void>;

  /**
   * Block until the specified resources have been loaded at least once.
   */
  waitForResources(actionContext: ActionContext<ResourceStateReturn<T[number]>>, resources: readonly ResourceNames<T>[]): Promise<void>;
}

/**
 * resourceWatchActions is a helper function to define the actions object.
 * @param resources Array of literals that satisfy ResourceType<T>.
 */
export function resourceWatchActions<const T extends readonly ResourceTypeLike[]>(module: string, resources: T):
ResourceWatchActionsReturn<T> {
  type State = ResourceStateReturn<T[number]>;

  let reconnectTimeout: ReturnType<typeof setTimeout> | undefined;

  return {
    async setupResourceWatch(actionContext: ActionContext<State>, options?: ResourceWatchActionsOptions<T>) {
      const { commit, state, dispatch, rootState } = actionContext;
      const rddState = rootState['rdd-connection'];

      while (!rddState.config.currentContext) {
        try {
          await dispatch('rdd-connection/fetchConfig', {}, { root: true });
        } catch (error) {
          // If any error occurs during fetching of config, retry forever until
          // the backend is up.
          // TODO: Once we have diagnostics, emit one here so the user can
          // deal with it.
          commit('rdd-connection/SET_ERROR', error, { root: true });
          console.error(error);
          await new Promise(resolve => setTimeout(resolve, 1_000));
        }
      }

      try {
        // Reset `_watchersInitialized`, so if this is a reconnect, the callers
        // can wait on it again.
        state._watchersInitialized.reset();
        for (const r of resources) {
          const resourceOptions = Object.assign({}, state._watchers[r.name]?.options, options ?? {});
          state._watchers[r.name]?.watcher?.close();
          const client = r.makeClient(rddState.config);
          const watcher = new Watcher(
            r.name,
            r.path(rddState.namespace),
            async() => { // listFn
            // If this throws, the `doneFn` callback gets called with the exception.
              const listOptions: ListResourceOptions<any> = {
                namespace:       rddState.namespace,
                state,
                connectionState: rddState,
                fieldSelector:   r.fieldSelector?.(actionContext),
                labelSelector:   r.labelSelector?.(actionContext),
              };
              const result = await r.list(client, listOptions);
              commit('rdd-connection/SET_ERROR', undefined, { root: true });
              return result;
            },
            async(error) => { // doneFn
              commit('rdd-connection/SET_ERROR', error, { root: true });
              await dispatch('rdd-connection/notifyDisconnected', null, { root: true });
              if (error) {
                resourceOptions.callback?.(error, r.name as ResourceNames<T>);
                console.error(`${ r.name }:`, error);
              } else {
                console.error(`${ r.name }: Closing connection without error`);
              }
              // The old config is no longer valid; clear it so when we set up
              // the next watch, it will be re-fetched.
              commit('rdd-connection/SET_CONFIG', new RDDClient.KubeConfig(), { root: true });
              clearTimeout(reconnectTimeout);
              const doReconnect = async() => {
                try {
                  await dispatch('setupResourceWatch', options);
                } catch (error) {
                  console.error(error);
                  commit('rdd-connection/SET_ERROR', error, { root: true });
                  // If we failed to connect, try again after a delay.
                  reconnectTimeout = setTimeout(doReconnect, 1_000);
                }
              };
              reconnectTimeout = setTimeout(doReconnect, 1_000);
            },
            rootState['rdd-connection'].watch,
            commit,
            undefined, // namespace
            () => r.labelSelector?.(actionContext),
            () => r.fieldSelector?.(actionContext),
          );
          const refCount = state._watchers[r.name]?.refCount ?? 0;
          commit('SET__WATCHERS', {
            ...state._watchers,
            [r.name]: { watcher, options: resourceOptions, refCount },
          } as any);
          if (refCount > 0) {
          // If the existing refcount is set (i.e. a reconnect), start watching
          // automatically.
            watcher.start();
          }
          const key = `${ module }/${ r.name }`;
          await dispatch('rdd-connection/registerDisconnectCallback',
            {
              key,
              callback: () => {
                watcher.close();
                dispatch('rdd-connection/registerDisconnectCallback', { key }, { root: true });
              },
            }, { root: true });
        }
        state._watchersInitialized.resolve();
      } catch (error) {
        state._watchersInitialized.reject(error);
        throw error;
      }
    },
    async watchResources(actionContext: ActionContext<State>, resources: ResourceNames<T>[]) {
      const { state, commit } = actionContext;
      await state._watchersInitialized;
      for (const resource of resources) {
        const watcherInfo = state._watchers[resource];
        if (!watcherInfo) {
          const error = new Error(`Action watchResources(${ resource }) called before setupWatch`);
          console.error(error);
          throw error;
        }
        let count = watcherInfo.refCount ?? 0;
        if (count < 0) {
          // Invalid reference count.
          console.error(`Action watchResources(${ resource }) called with invalid reference count ${ count }`);
          count = 0;
        }
        commit('SET__WATCHERS', {
          ...state._watchers,
          [resource]: {
            ...watcherInfo,
            refCount: count + 1,
          },
        } as any);
        if (count === 0) {
          watcherInfo.watcher.start();
        }
      }
    },
    async unwatchResources(actionContext: ActionContext<State>, resources: ResourceNames<T>[]) {
      const { state, commit } = actionContext;
      await state._watchersInitialized;
      for (const resource of resources) {
        const watcherInfo = state._watchers[resource];
        if (!watcherInfo) {
          console.error(`Action unwatchResources(${ resource }) called before setupWatch`);
          continue;
        }
        const count = watcherInfo.refCount ?? 0;
        if (count < 1) {
          // Invalid reference count.
          console.error(`Action unwatchResources(${ resource }) called with invalid reference count ${ count }`);
          continue;
        }
        commit('SET__WATCHERS', {
          ...state._watchers,
          [resource]: {
            ...watcherInfo,
            refCount: count - 1,
          },
        } as any);
        if (count === 1) {
          watcherInfo.watcher.close();
        }
      }
    },
    async waitForResources(actionContext: ActionContext<State>, resources: ResourceNames<T>[]) {
      const { state } = actionContext;
      await state._watchersInitialized;
      await Promise.all(resources.map(async(resource) => {
        const watcherInfo = state._watchers[resource];
        if (!watcherInfo) {
          const error = new Error(`Action waitForResources(${ resource }) called before setupWatch`);
          console.error(error);
          throw error;
        }
        await watcherInfo.watcher.loaded;
      }));
    },
  };
}

const errorManuallyStopped = new Error('Manually stopped');

/**
 * A watcher is used to watch a resource type.
 * This is kept in the per-module `_watchers` state.
 */
class Watcher<
  K extends string,
  T extends RDDClient.KubernetesObject,
> {
  #type:        K;
  #namespace?:  string;
  #items:       readonly T[] = [];
  #loaded = Latch();
  #notifyDelay: ReturnType<typeof setTimeout> | undefined;
  #commit:      Commit<any>;
  #doneFn:      (error?: any) => void;
  #watcher:     RDDClient.ListWatch<T>;

  /**
   * Create a new watcher.
   * The caller is responsible for calling `start()`.
   */
  constructor(
    type: K,
    path: string,
    listFn: () => Promise<RDDClient.KubernetesListObject<T>>,
    doneFn: (error?: any) => void,
    watch: RDDClient.Watch,
    commit: Commit<any>,
    namespace?: string,
    labelSelector?: () => string | undefined,
    fieldSelector?: () => string | undefined,
  ) {
    this.#type = type;
    this.#namespace = namespace;
    this.#commit = commit;
    this.#doneFn = doneFn;
    // Attach a fallback catch handler so that initial disconnects do not show
    // as errors.
    this.#loaded.catch(() => { /* ignore */ });
    this.#watcher = new RDDClient.ListWatch<T>(
      path,
      watch,
      listFn,
      false,
      labelSelector,
      fieldSelector,
    );
    this.#watcher.on('change', this.onChange.bind(this));
    this.#watcher.on('connect', this.onChange.bind(this));
    this.#watcher.on('error', (err) => {
      if (Object.is(err, errorManuallyStopped)) {
        // The watcher errored out because we called stop(); do not propagate
        // this as an actual error.
        return;
      } else if (err.code === 429) {
        // We can get this if we request too early (when the backend restarts),
        // with a message of "storage is (re)initializing".  Just retry.
        let delay = 0.1;
        try {
          const body = JSON.parse(err.body);
          delay = body?.details?.retryAfterSeconds ?? delay;
        } catch { /* ignore */ }
        setTimeout(() => this.start(), delay * 1_000);
        return;
      }
      // `err` is an object that calls `toString()` on `console.log`, so we
      // need to re-convert it to a plain object for better debugging.
      console.debug(`${ type } watch error`, JSON.parse(JSON.stringify(err)));
      this.#loaded.reject(err);
      doneFn(err);
    });
  }

  start() {
    this.#watcher.start().catch(err => {
      console.debug(`Watch ${ this.#type } ended:`, err);
      this.#doneFn(err);
      this.#loaded.reject(err);
    });
  }

  get loaded(): Promise<void> {
    return this.#loaded;
  }

  protected onChange() {
    // ListWatch calls this synchronously once per element, but we only need to
    // batch the results, so set up a delay that gets triggered soon.  Ideally
    // we'd use `queueMicrotask`, but that doesn't allow us to check if it's
    // already queued.
    if (this.#notifyDelay) {
      return;
    }
    this.#notifyDelay = setTimeout(() => {
      const key = `SET_${ UpperSnakeCase(this.#type) }` as `SET_${ UpperSnakeCase<K> }`;
      this.#notifyDelay = undefined;
      this.#items = this.#watcher?.list(this.#namespace) ?? [];
      this.#commit(key, this.#items);
      this.#loaded.resolve();
    }, 0);
  }

  close() {
    this.#watcher.stop(errorManuallyStopped);
  }
}
