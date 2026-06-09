// This file contains the state required to maintain a connection to the Rancher
// Desktop Daemon.

import { markRaw } from 'vue';
import { MutationTree, Plugin } from 'vuex';

import type { RootState } from '@pkg/entry/store';
import { ActionContext, ActionTree, Commit, MutationsType } from '@pkg/store/ts-helpers';
import ipcRenderer from '@pkg/utils/ipcRenderer';
import Latch from '@pkg/utils/latch';
import { UnionToIntersection, UpperSnakeCase } from '@pkg/utils/typeUtils';
import * as RDDClient from '@rdd-client';

/**
 * RDDState describes the connection to the Rancher Desktop Daemon.
 */
export interface RDDConnectionState {
  /** config is the Kubernetes configuration to use. */
  config:              RDDClient.KubeConfig;
  error:               any;
  watch:               RDDClient.Watch;
  /** A mapping of module name to the names of resources set up for watching. */
  namespacedResources: Record<string, Set<string>>;
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
    namespacedResources: {},
    namespace:           'default',
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
  SET_NAMESPACE(state, namespace) {
    state.namespace = namespace;
  },
  /**
   * Mark a namespaced resource as being watched; this is used to track
   * re-watching resources on connect and when the Kubernetes namespace changes.
   * @param moduleName the name of the Vuex module.
   */
  addNamespacedResource(state, [module, resource]: [string, string]) {
    state.namespacedResources[module] ??= new Set();
    state.namespacedResources[module].add(resource);
  },
} satisfies MutationsType<RDDConnectionState> & MutationTree<RDDConnectionState>;

/**
 * fetchConfigPromise is used to ensure that multiple concurrent calls to
 * `fetchConfig` only runs once; this is necessary if a disconnect causes
 * multiple watchers to error out at once.
 */
let fetchConfigPromise: Promise<RDDClient.KubeConfig> | undefined;

/** Vuex actions for managing the RDD connection. */
export const actions = {
  /**
   * Fetch the Kubernetes configuration from the backend and update the state.
   * This may be called multiple times concurrently.
   * This never throws; instead, it blocks until success.
   * After the first success, it always returns the same config.
   */
  async fetchConfig({ commit }): Promise<RDDClient.KubeConfig> {
    if (!fetchConfigPromise) {
      fetchConfigPromise = (async() => {
        while (true) {
          try {
            const config = await ipcRenderer.invoke('rdd/kube-config');
            const kubeConfig = new RDDClient.KubeConfig();

            kubeConfig.loadFromString(config);
            commit('SET_CONFIG', kubeConfig);

            return kubeConfig;
          } catch {
            // Try again
            await new Promise(resolve => setTimeout(resolve, 1_000));
          }
        }
      })();
    }
    return fetchConfigPromise;
  },
} satisfies ActionTree<RDDConnectionState, any, typeof mutations>;

export const plugins: Plugin<RootState>[] = [
  /** Vuex plugin to automatically fetch connection configuration on startup. */
  function(store) {
    store.dispatch('rdd-connection/fetchConfig').catch(ex => {
      console.error(ex);
    });
  },
  /** Vuex plugin to re-watch resources on Kubernetes namespace change. */
  function(store) {
    let currentNamespace = store.state['rdd-connection'].namespace;
    store.watch(
      (state) => state['rdd-connection'].namespace,
      (newNamespace) => {
        if (newNamespace && newNamespace !== currentNamespace) {
          currentNamespace = newNamespace;

          const { namespacedResources } = store.state['rdd-connection'];
          for (const [module, resources] of Object.entries(namespacedResources)) {
            if (resources.size) {
              store.dispatch(`${ module }/rewatchResources`, Array.from(resources))
                .catch(error => {
                  console.error(`Failed to rewatch resources for module ${ module }:`, error);
                });
            }
          }
        }
      },
    );
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
 * Intersects all elements of a mapped type indexed by number.
 * `{ 0: A, 1: B, 2: C }` becomes `A & B & C`.
 */
type IntersectMapped<T> = UnionToIntersection<T[keyof T & number]>;

/**
 * ResourceTypeLike is a loose structural type used for parameter constraints.
 * It avoids invariance issues that arise from using ResourceType directly.
 */
interface ResourceTypeLike {
  /** The name of the state, typically plural. */
  name:           string;
  /** The name of the resource kind, used in the API name. */
  type?:          string;
  /**
   * The API path to the resources, given the Kubernetes namespace.  The
   * namespace is required; however, implementations may ignore it.
   */
  path:           (namespace: string) => string;
  /** Optional label selector to filter resources. */
  labelSelector?: (context: any) => string | undefined;
  /** Optional field selector to filter resources. */
  fieldSelector?: (context: any) => string | undefined;
  /** A function which returns the type of client needed to list the resource. */
  makeClient:     (config: RDDClient.KubeConfig) => any;
  /**
   * A function which lists the resource.
   * By default, the client's `list[Namespaced]<Type>` function is used.
   */
  list?:          (client: any, options: ListResourceOptions<any>) => Promise<RDDClient.KubernetesListObject<any>>;
  /**
   * Indicates that the resource needs to be re-watched when the Kubernetes namespace changes.
   * This is only needed if `list` is given.
   */
  namespaced?:    boolean;
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
  name:           StateName;
  type?:          TypeName;
  path:           (namespace: string) => string;
  labelSelector?: (context: any) => string | undefined;
  fieldSelector?: (context: any) => string | undefined;
  makeClient:     (config: RDDClient.KubeConfig) => C,
  list?:          (client: C, options: ListResourceOptions<ResourceStateItem<StateName, ItemType<C, TypeName>>>) => Promise<RDDClient.KubernetesListObject<ItemType<C, TypeName>>>;
  namespaced?:    boolean;
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
   * Restart watching the specified resources, if they are being watched.  This
   * is used when the resource definition will produce different results (e.g.
   * the label selector changes).
   */
  rewatchResources(actionContext: ActionContext<ResourceStateReturn<T[number]>>, resources: readonly ResourceNames<T>[]): Promise<void>;

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

  return {
    async setupResourceWatch(actionContext: ActionContext<State>, options?: ResourceWatchActionsOptions<T>) {
      const { commit, state, dispatch, rootState } = actionContext;
      const rddState = rootState['rdd-connection'];

      // Ensure that the connection configuration is available.  If we already
      // have a config, this returns immediately.
      await dispatch('rdd-connection/fetchConfig', undefined, { root: true });

      if (state._watchersInitialized.settled) {
        // This should never happen, but guard against it just in case.
        throw new Error('setupResourceWatch called more than once');
      }

      try {
        for (const r of resources) {
          const resourceOptions = Object.assign({}, state._watchers[r.name]?.options, options ?? {});
          let reconnectTimeout: ReturnType<typeof setTimeout> | undefined;

          state._watchers[r.name]?.watcher?.close();
          const client = r.makeClient(rddState.config);
          const listFn: (client: any, options: ListResourceOptions<typeof state>) => any = (() => {
            const type = r.type ?? r.name.replace(/s$/, '').replace(/^./, s => s.toUpperCase());
            if (r.list) {
              if (r.namespaced) {
                commit('rdd-connection/addNamespacedResource', [module, r.name], { root: true });
              }
              return r.list;
            } else if (`listNamespaced${ type }` in client) {
              commit('rdd-connection/addNamespacedResource', [module, r.name], { root: true });
              return (client, options) => {
                return client[`listNamespaced${ type }`]({
                  namespace:     options.namespace,
                  fieldSelector: options.fieldSelector,
                  labelSelector: options.labelSelector,
                });
              };
            } else if (`list${ type }` in client) {
              return (client, options) => {
                return client[`list${ type }`]({
                  fieldSelector: options.fieldSelector,
                  labelSelector: options.labelSelector,
                });
              };
            } else {
              throw new Error(`Client ${ r.makeClient.name } does not have a list function for resource type ${ module }/${ type }`);
            }
          })();
          const watcher = new Watcher(
            r.name,
            () => r.path(rootState['rdd-connection'].namespace),
            async() => { // listFn
              // If this throws, the `doneFn` callback gets called with the exception.
              const listOptions: ListResourceOptions<any> = {
                namespace:       rootState['rdd-connection'].namespace,
                state,
                connectionState: rddState,
                fieldSelector:   r.fieldSelector?.(actionContext),
                labelSelector:   r.labelSelector?.(actionContext),
              };
              const result = await listFn(client, listOptions);
              commit('rdd-connection/SET_ERROR', undefined, { root: true });
              return result;
            },
            (error) => { // doneFn
              commit('rdd-connection/SET_ERROR', error, { root: true });
              if (error) {
                resourceOptions.callback?.(error, r.name);
                console.error(`${ r.name }:`, error);
              } else {
                console.debug(`${ r.name }: Closing connection without error`);
                if (state._watchers[r.name]?.refCount < 1) {
                  return; // Do not reconnect on stop.
                }
              }
              const doReconnect = async() => {
                // None of these are expected to throw.
                await dispatch('rdd-connection/fetchConfig', undefined, { root: true });
                watcher.close();
                if (state._watchers[r.name]?.refCount > 0) {
                  watcher.start();
                }
              };
              clearTimeout(reconnectTimeout);
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
        if (!watcherInfo?.watcher) {
          const error = new Error(`Action watchResources(${ resource }) called before setupResourceWatch`);
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
          console.error(`Action unwatchResources(${ resource }) called before setupResourceWatch`);
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
          const error = new Error(`Action waitForResources(${ resource }) called before setupResourceWatch`);
          console.error(error);
          throw error;
        }
        await watcherInfo.watcher.loaded;
      }));
    },
    async rewatchResources(actionContext: ActionContext<State>, resources: ResourceNames<T>[]) {
      const { state } = actionContext;
      await state._watchersInitialized;
      for (const resource of resources) {
        const watcherInfo = state._watchers[resource];
        if (!watcherInfo) {
          const error = new Error(`Action rewatchResources(${ resource }) called before setupResourceWatch`);
          console.error(error);
          throw error;
        }
        // Only restart the watcher if it's currently active.
        if (watcherInfo.refCount > 0) {
          watcherInfo.watcher.close();
          watcherInfo.watcher.start();
          // We may temporarily have stale data before the new watch applies;
          // that is acceptable, and reduces flickering.
        }
      }
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
  #type:           K;
  #path:           () => string;
  #listFn:         () => Promise<RDDClient.KubernetesListObject<T>>;
  #doneFn:         (error?: any) => void;
  #watch:          RDDClient.Watch;
  #commit:         Commit<any>;
  #namespace?:     string;
  #labelSelector?: () => string | undefined;
  #fieldSelector?: () => string | undefined;
  #items:          readonly T[] = [];
  #loaded = Latch();
  #notifyDelay:    ReturnType<typeof setTimeout> | undefined;
  #watcher:        RDDClient.ListWatch<T> | undefined;
  #restartTimeout: ReturnType<typeof setTimeout> | undefined;

  /**
   * Create a new watcher.
   * The caller is responsible for calling `start()`.
   */
  constructor(
    type: K,
    path: () => string,
    listFn: () => Promise<RDDClient.KubernetesListObject<T>>,
    doneFn: (error?: any) => void,
    watch: RDDClient.Watch,
    commit: Commit<any>,
    namespace?: string,
    labelSelector?: () => string | undefined,
    fieldSelector?: () => string | undefined,
  ) {
    this.#type = type;
    this.#path = path;
    this.#listFn = listFn;
    this.#doneFn = doneFn;
    this.#watch = watch;
    this.#commit = commit;
    this.#namespace = namespace;
    this.#labelSelector = labelSelector;
    this.#fieldSelector = fieldSelector;
    // Attach a fallback catch handler so that initial disconnects do not show
    // as errors.
    this.#loaded.catch(() => { /* ignore */ });

    // Create a new ListWatch instance in `start()`, so that we don't have to
    // worry about any issues from ListWatch restart.
  }

  start() {
    // Stop any existing watcher before starting a new one.
    this.#watcher?.stop(errorManuallyStopped);
    const watcher = new RDDClient.ListWatch<T>(
      this.#path(),
      this.#watch,
      this.#listFn,
      false,
      this.#labelSelector,
      this.#fieldSelector,
    );
    this.#watcher = watcher;
    watcher.on('change', () => {
      // Only trigger a change if this is the current watcher.
      if (Object.is(watcher, this.#watcher)) {
        this.onChange();
      }
    });
    watcher.on('connect', () => {
      // Only trigger a change if this is the current watcher.
      if (Object.is(watcher, this.#watcher)) {
        this.onChange();
      }
    });
    watcher.on('error', (err) => {
      if (!Object.is(watcher, this.#watcher)) {
        // This error is from a stale watcher; ignore it.
        return;
      }
      clearTimeout(this.#restartTimeout);
      this.#loaded.reset();
      this.#loaded.catch(() => { /* ignore */ });
      if (Object.is(err, errorManuallyStopped)) {
        // The watcher errored out because we called stop(); do not propagate
        // this as an actual error.
        this.#loaded.resolve();
        this.#doneFn();
        return;
      } else if (err.statusCode === 429) {
        // We can get this if we request too early (when the backend restarts),
        // with a message of "storage is (re)initializing".  Just retry.
        let delay = 0.1;
        try {
          const body = JSON.parse(err.body);
          delay = body?.details?.retryAfterSeconds ?? delay;
        } catch { /* ignore */ }
        this.#restartTimeout = setTimeout(() => this.start(), delay * 1_000);
        return;
      }
      // this.#doneFn(err) logs the error already, no need to do so here.
      this.#loaded.reject(err);
      this.#doneFn(err);
    });
    // `#watcher.start()` returns a promise that is only resolved when the
    // connection ends, so do not await on it here.
    this.#watcher.start().catch(err => {
      if (!Object.is(watcher, this.#watcher)) {
        // A stale watcher ends; ignore it.
        return;
      }
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
    clearTimeout(this.#restartTimeout);
    clearTimeout(this.#notifyDelay);
    this.#notifyDelay = undefined;
    this.#loaded.reset();
    this.#loaded.catch(() => { /* ignore */ });
    return this.#watcher?.stop(errorManuallyStopped);
  }
}
