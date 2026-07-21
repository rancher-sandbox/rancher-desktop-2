import type { Modules, RootState } from '@pkg/entry/store';
import type { UnionToIntersection, UpperSnakeCase } from '@pkg/utils/typeUtils';

import type { CommitOptions, Dispatch, MutationTree, Store } from 'vuex';

/**
 * MutationsType is used to describe the type that `mutations` should have.
 * This has a `SET_` method per property in `State`, that takes a payload of the
 * correct type.  Note that we may have additional mutations available; typically
 * this is used as `const mutations = { ... } satisfies MutationsType<State>`.
 */
export type MutationsType<T> = {
  [key in keyof T as `SET_${ UpperSnakeCase<key> }`]?: (state: T, payload: T[key]) => any;
};

/**
 * MutationsPayloadType converts from a MutationsType to a type with the same
 * keys but just the payload as the value.
 */
type MutationsPayloadType<M> = {
  [key in keyof M]: M[key] extends (...args: any) => any ? Parameters<M[key]>[1] : never;
};

type moduleNames = keyof Modules;
type mutationTypes<MN extends moduleNames> = keyof Modules[MN]['mutations'] & string;
type fullMutationType<MN extends moduleNames, MT extends mutationTypes<MN>> = `${ MN }/${ MT }`;
type moduleName<S> = {
  [MN in moduleNames]:
  S extends ReturnType<Modules[MN]['state']>
    ? ReturnType<Modules[MN]['state']> extends S
      ? MN
      : never // MN ⊉ S; this is not the right module.
    : never // S ⊉ MN; this is not the right module.
}[moduleNames];
type moduleFor<S> = moduleName<S> extends never ? never : Modules[moduleName<S>];
type mutationsFor<S> = moduleFor<S> extends { mutations: any } ? moduleFor<S>['mutations'] : MutationsType<S> & MutationTree<S>;
type gettersFor<S> = moduleFor<S> extends { getters: any } ? moduleFor<S>['getters'] : GetterTree<S, any>;

export type RootGetters = UnionToIntersection<{
  [MN in moduleNames]: Modules[MN] extends { getters: any } ? {
    [getter in keyof Modules[MN]['getters'] & string as `${ MN }/${ getter }`]:
    Modules[MN]['getters'][getter] extends (...args: any) => infer R ? R : never;
  } : never;
}[moduleNames]>;

/**
 * ActionContext is the first argument for an action.  We only declare the
 * subset we currently need.  We're not using the types from Vuex as that does
 * not provide typing to match the mutations.
 */
export interface ActionContext<S, M = MutationsType<S>, G = GetterTree<S>> {
  commit:      Commit<M>;
  dispatch:    Dispatch;
  state:       S;
  rootState:   RootState;
  getters:     { [key in keyof G]: G[key] extends (...args: any) => any ? ReturnType<G[key]> : never };
  rootGetters: RootGetters;
}

export interface Commit<M> {
  <mutationType extends keyof M>(type: mutationType, payload: MutationsPayloadType<M>[mutationType], commitOptions?: Omit<CommitOptions, 'root'>): void;
  <mutationType extends keyof M>(type: mutationType, payload: MutationsPayloadType<M>[mutationType], commitOptions: CommitOptions & { root: false }): void;
  <moduleName extends keyof Modules,
    mutationType extends mutationTypes<moduleName>,
    payloadType extends MutationsPayloadType<Modules[moduleName]['mutations']>[mutationType],
  >(type: fullMutationType<moduleName, mutationType>, payload: payloadType, commitOptions: CommitOptions & { root: true }): void;
}

// Copies from the vuex definition, but using our override ActionContext above.
type ActionHandler<S, R, M, G> = (this: Store<R>, context: ActionContext<S, M, G>, payload?: any) => any;
export interface ActionObject<S, R, M, G> {
  root?:   boolean;
  handler: ActionHandler<S, R, M, G>;
}
type Action<S, R, M, G> = ActionHandler<S, R, M, G> | ActionObject<S, R, M, G>;

export type ActionTree<
  S,
  R = RootState,
  M extends MutationsType<S> & MutationTree<S> = MutationsType<S> & MutationTree<S>,
  G extends GetterTree<S, any> = GetterTree<S, any>,
> = moduleName<S> extends never
  ? Record<string, Action<S, R, M, G>>
  : Record<string, Action<S, RootState, mutationsFor<S>, gettersFor<S>>>;

// Unfortunately, we can't use the `RootGetters` type here, as that would lead to
// a circular definition, since this is used to define the per-module getters.
export type GetterTree<S, R = RootState, G = any> =
  Record<string, (state: S, getters: G, rootState: R, rootGetters: Record<`${ string }/${ string }`, any>) => any>;
