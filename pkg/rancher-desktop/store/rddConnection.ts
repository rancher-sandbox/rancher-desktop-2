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

/** Vuex actions for managing the RDD connection. */
export const actions = {
  async fetchConfig({ commit, dispatch }) {
    try {
      const config = await ipcRenderer.invoke('rdd/kube-config');
      const kubeConfig = new RDDClient.KubeConfig();

      kubeConfig.loadFromString(config);
      commit('SET_CONFIG', kubeConfig);
      await dispatch('notifyDisconnected');
      commit('SET_DISCONNECT_CALLBACKS', {});
      commit('SET_ERROR', undefined);

      return kubeConfig;
    } catch (ex) {
      commit('SET_ERROR', ex);
      throw ex;
    }
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
type ResourceNames<T extends readonly ResourceTypeLike[]> = {
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
    options:  ResourceWatchActionsOptions | undefined,
  }>;
/**
 * ResourceStateItem defines the state object derived from one specific resource type.
 */
type ResourceStateItem<Key extends string, T extends RDDClient.KubernetesObject> =
  Record<Key, null | T[]> & { _watchers: ResourceStateWatcher<Key, T> };

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
    _watchers: {},
    ...Object.fromEntries(resources.map(r => [r.name, null])),
  } as ReturnType<typeof resourceState<T>>;
}

type ResourceMutationsReturn<R> =
  R extends ResourceType<infer C, infer N extends string, infer TypeName extends string>
    ? {
      [key in N as `SET_${ UpperSnakeCase<key> }`]:
      (state: ResourceStateItem<N, ItemType<C, TypeName>>, payload: ItemType<C, TypeName>[] | null) => void }
      & {
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

type ResourceActionName<Action extends string, StateName extends string> = `${ Action }${ Capitalize<StateName> }`;

export function resourceActionName<Action extends string, StateName extends string>(action: Action, stateName: StateName): ResourceActionName<Action, StateName> {
  return action + stateName.replace(/^./, c => c.toUpperCase()) as ResourceActionName<Action, StateName>;
}

/**
 * ResourceWatchActionsOptions describes options the caller may pass to start
 * watching a resource.
 */
interface ResourceWatchActionsOptions {
  /** Callback that is invoked when an error occurs. */
  callback?:        (error: Error) => void,
  /** The initial reference count. */
  initialRefCount?: number,
}

type ResourceWatchActionsReturnItem<StateName extends string, StateItem extends RDDClient.KubernetesObject> = {
  /**
   * Set up watching for the resource type.  This does not automatically
   * start watching; calling the `watch<T>` action is required to
   * start the watch.
   */
  [key in StateName as ResourceActionName<'setupWatch', key>]: (
    context: ActionContext<ResourceStateItem<StateName, StateItem>>,
    options?: ResourceWatchActionsOptions,
  ) => Promise<void>;
} & {
  [key in StateName as ResourceActionName<'watch', key>]: (
    context: ActionContext<ResourceStateItem<StateName, StateItem>>,
  ) => void;
} & {
  [key in StateName as ResourceActionName<'unwatch', key>]: (
    context: ActionContext<ResourceStateItem<StateName, StateItem>>,
  ) => void;
} & {
  [key in StateName as ResourceActionName<'waitForWatch', key>]: () => Promise<void>;
};

/** ResourceWatchActionsReturn defines the return type of resourceWatchActions(). */
type ResourceWatchActionsReturn<R> =
  R extends ResourceType<infer C, infer StateName extends string, infer TypeName extends string>
    ? ResourceWatchActionsReturnItem<StateName, ItemType<C, TypeName>>
    : never;

/**
 * resourceWatchActions is a helper function to define the actions object.
 * @param resources Array of literals that satisfy ResourceType<T>.
 */
export function resourceWatchActions<const T extends readonly ResourceTypeLike[]>(resources: T):
IntersectMapped<{ [K in keyof T]: ResourceWatchActionsReturn<T[K]> }> {
  type State = ResourceStateReturn<T[number]>;
  type StateName = ResourceNames<T>;

  const result: Partial<Record<
    ResourceActionName<'setupWatch' | 'watch' | 'unwatch' | 'waitForWatch', StateName>,
    unknown >> = {};

  for (const r of resources) {
    result[resourceActionName('setupWatch', r.name as ResourceNames<T>)] =
      async(actionContext: ActionContext<State>, options?: ResourceWatchActionsOptions) => {
        const { commit, state, dispatch, rootState } = actionContext;
        const rddState = rootState['rdd-connection'];

        if (!rddState.config.currentContext) {
          await dispatch('rdd-connection/fetchConfig', {}, { root: true });
        }
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
              options?.callback?.(error);
              console.error(`${ r.name }:`, error);
            } else {
              console.error(`${ r.name }: Closing connection without error`);
            }
            setTimeout(async() => {
              await dispatch('rdd-connection/fetchConfig', {}, { root: true });
              await dispatch(resourceActionName('setupWatch', r.name), {
                ...options,
                initialRefCount: state._watchers[r.name]?.refCount ?? 0,
              });
            }, 1_000);
          },
          rootState['rdd-connection'].watch,
          commit,
          undefined, // namespace
          () => r.labelSelector?.(actionContext),
          () => r.fieldSelector?.(actionContext),
        );
        commit('SET__WATCHERS', {
          ...state._watchers,
          [r.name]: { watcher, options, refCount: options?.initialRefCount ?? 0 },
        } as any);
        if ((options?.initialRefCount ?? 0) > 0) {
          // If the initial refcount is set (i.e. a reconnect), start watching
          // automatically.
          watcher.start();
        }

        await dispatch('rdd-connection/registerDisconnectCallback',
          {
            key:      r.name,
            callback: () => {
              watcher.close();
              dispatch('rdd-connection/registerDisconnectCallback', { key: r.name }, { root: true });
            },
          }, { root: true });
      };
    result[resourceActionName('watch', r.name as ResourceNames<T>)] =
      (actionContext: ActionContext<State>) => {
        const { state, commit } = actionContext;
        const watcherInfo = state._watchers[r.name];
        if (!watcherInfo) {
          console.error(`Action ${ resourceActionName('watch', r.name) } called before setupWatch`);
          return;
        }
        let count = watcherInfo.refCount ?? 0;
        if (count < 0) {
          // Invalid reference count.
          console.error(`Action ${ resourceActionName('watch', r.name) } called with invalid reference count ${ count }`);
          count = 0;
        }
        commit('SET__WATCHERS', {
          ...state._watchers,
          [r.name]: {
            ...watcherInfo,
            refCount: count + 1,
          },
        } as any);
        if (count === 0) {
          watcherInfo.watcher.start();
        }
      };
    result[resourceActionName('unwatch', r.name as ResourceNames<T>)] =
      (actionContext: ActionContext<State>) => {
        const { state, commit } = actionContext;
        const watcherInfo = state._watchers[r.name];
        if (!watcherInfo) {
          console.error(`Action ${ resourceActionName('unwatch', r.name) } called before setupWatch`);
          return;
        }
        const count = watcherInfo.refCount ?? 0;
        if (count < 1) {
          // Invalid reference count.
          console.error(`Action ${ resourceActionName('unwatch', r.name) } called with invalid reference count ${ count }`);
          return;
        }
        commit('SET__WATCHERS', {
          ...state._watchers,
          [r.name]: {
            ...watcherInfo,
            refCount: Math.max(0, count - 1),
          },
        } as any);
        if (count === 1) {
          watcherInfo.watcher.close();
        }
      };
    result[resourceActionName('waitForWatch', r.name as ResourceNames<T>)] =
      async(actionContext: ActionContext<State>) => {
        const { state, dispatch } = actionContext;

        while (true) {
          const watcher = state._watchers[r.name]?.watcher;
          if (watcher) {
            return await watcher.loaded;
          }

          // Watcher isn't set up yet; make sure it is.
          await dispatch(resourceActionName('setupWatch', r.name));
        }
      };
  }

  return result as ReturnType<typeof resourceWatchActions<T>>;
}

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
      if (err.code === 429) {
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
    this.#watcher.stop();
  }
}
