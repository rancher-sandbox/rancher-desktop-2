import _ from 'lodash';
import { toRaw } from 'vue';
import { MutationTree, Plugin, Store } from 'vuex';

import { RootState } from '@pkg/entry/store';
import { ActionContext, ActionTree, GetterTree, MutationsType } from '@pkg/store/ts-helpers';
import Latch from '@pkg/utils/latch';
import { FieldType, RecursiveLeafKeys, RecursiveReadonly } from '@pkg/utils/typeUtils';
import * as RDDClient from '@rdd-client';

/** The field manager we set for preferences we modify. */
const fieldManager = 'rancher-desktop-app';

type App = RDDClient.IoRancherdesktopAppV1alpha1App;
type AppSpec = RDDClient.IoRancherdesktopAppV1alpha1AppSpec;

/**
 * PreferencesState is the state for preferences the user has changed but not
 * yet committed.  This is used for the preferences dialog, where the user can
 * change preferences and then either commit or discard them.
 */
type PreferencesState = ReturnType<typeof state>;

/**
 * Changes is a type that represents the changes made to the preferences.  It is
 * a mapping of dot-separated keys to the new values.
 * @note It is therefore not possible for a key to contain a dot.
 */
type Changes = {
  [K in RecursiveLeafKeys<AppSpec>]?: FieldType<AppSpec, K>;
};

/**
 * JSON-Patch structure, per RFC 6902.
 */
type JSONPatch = ({
  op:    'add' | 'replace',
  path:  string,
  value: any,
} | { op: 'remove', path: string })[];

/**
 * SerializedRequestMiddleware is a middleware that serializes requests, so that
 * only one request is in flight at a time.  This is used to ensure that we don't
 * have multiple requests to patch the app in flight at the same time, which can
 * cause race conditions and validation errors.
 */
class SerializedRequestMiddleware implements RDDClient.ObservableMiddleware {
  private _controller?: AbortController;
  readonly abortError:  Error;

  constructor(name: string) {
    this.abortError = new Error(`New ${ name } request was made`);
  }

  pre(context: RDDClient.RequestContext): RDDClient.Observable<RDDClient.RequestContext> {
    // It is safe to call `abort` on an AbortController that has already aborted.
    this._controller?.abort(this.abortError);
    this._controller = new AbortController();
    context.setSignal(this._controller.signal);
    return new RDDClient.Observable(Promise.resolve(context));
  }

  post(context: RDDClient.ResponseContext): RDDClient.Observable<RDDClient.ResponseContext> {
    return new RDDClient.Observable(Promise.resolve(context));
  }

  /** Aborts the current request, if any. */
  abort() {
    this._controller?.abort(this.abortError);
    this._controller = undefined;
  }

  /** Returns the current AbortSignal, for testing use only. */
  get signal() {
    return this._controller?.signal;
  }
}
/** modifySerializer is used to serialize modify requests. */
const modifySerializer = new SerializedRequestMiddleware('modify');
/** commitSerializer is used to serialize commit requests. */
const commitSerializer = new SerializedRequestMiddleware('commit');

/**
 * pendingCommit is unresolved when there is a commit in progress; this is used
 * to prevent concurrent calls to modify().
 */
const pendingCommit = Latch();
pendingCommit.resolve();

export const state = () => ({
  /** The list of changes that have been made but not yet applied. */
  changes:           {} as Changes,
  /** Error status is a V1Status object representing validation issues for the preferences. */
  errorStatus:       undefined as RDDClient.V1Status | undefined,
});

export const mutations = {
  SET_CHANGES(state, changes) {
    state.changes = changes;
  },
  SET_ERROR_STATUS(state, errorStatus) {
    state.errorStatus = errorStatus;
  },
} satisfies MutationsType<PreferencesState> & MutationTree<PreferencesState>;

export const getters = {
  committed: (_state, _getters, _rootState, rootGetters) => {
    return (rootGetters['rdd/app']?.spec ?? {}) as Required<RecursiveReadonly<AppSpec>>;
  },
  /**
   * Preferences is the desired preferences, including uncommitted changes.
   */
  preferences: (state, getters) => {
    const modified = structuredClone(toRaw(getters.committed));
    for (const [key, value] of Object.entries(state.changes)) {
      _.set(modified, key, value);
    }
    return modified as Required<RecursiveReadonly<AppSpec>>;
  },
  /**
   * Whether there are uncommitted changes.
   */
  isDirty: (state) => {
    return Object.keys(state.changes).length > 0;
  },
  canApply: (state, getters) => {
    return getters.isDirty && !state.errorStatus;
  },
  isPreferenceLocked: () => (_key: RecursiveLeafKeys<AppSpec>) => {
    // TODO: deployment profiles https://github.com/rancher-sandbox/rancher-desktop-2/issues/513
    return false;
  },
} satisfies GetterTree<PreferencesState>;

/**
 * appMissingStatus is a V1Status object that represents the error that occurs
 * when the app is missing.  This is used to fake a status so we can report the
 * error to the alert component easier.
 * @note This is only exported for testing; it should not be used outside of this module.
 */
export const appMissingStatus: RDDClient.V1Status = {
  details: {
    causes: [
      {
        field:   'metadata.name',
        message: 'Cannot commit preferences: app is missing',
        reason:  'NotFound',
      },
    ],
  },
};

export const actions = {
  /**
   * Modify the preferences with the given change, and validate the result.
   * This does not commit the changes; it only updates the preferences in the store.
   * @param key The key of the preference to modify, in dot notation.
   * @param value The new value for the preference.
   * @note If a commit is in progress, this waits for that to finish first.
   */
  async modify(context, { key, value }: { key: RecursiveLeafKeys<AppSpec>, value: FieldType<AppSpec, typeof key> }) {
    // Apply the changes immediately, so the UI can reflect the new state.
    recordChange(context, { key, value });

    const { state } = context;
    // Wait for any in-progress commit to finish, to avoid validating a state
    // that will never be committed.
    await pendingCommit;

    // Request validation; note that this may be different from the state we
    // originally requested, because `state.changes` may have been modified by
    // the `commit` that finished.  This is fine, as what we actually care about
    // is whether doing a `commit` will be accepted.
    await patchApp(context, state.changes, modifySerializer, 'All');
  },

  /**
   * Write the given preference to the backend directly, updating the store and
   * committing in one action.  This is used for preferences that do not go
   * through the normal dialog.  Existing pending changes will not be committed.
   * @returns Whether the commit was successful.
   * @note Calling this will abort any in-flight validation requests, as well as
   *       any in-flight commit requests.
   */
  async writeNow(context, { key, value }: { key: RecursiveLeafKeys<AppSpec>, value: FieldType<AppSpec, typeof key> }) {
    // Update `state.changes` in case the pref dialog is open.
    recordChange(context, { key, value });

    const result = await commitChanges(this, context, { [key]: value });

    if (!result) {
      // The change was not applied; ensure the UI reflects the actual stored
      // values.
      const { [key]: _unused, ...modified } = context.state.changes;
      context.commit('SET_CHANGES', modified);
    }

    return result;
  },

  /**
   * Commit the current set of changes to the backend.
   * @returns Whether the commit was successful.
   * @note If a commit is already in progress, the previous call will be aborted.
   * @note Calling this will abort any in-flight validation requests.
   */
  commit(context): Promise<boolean> {
    const { state } = context;
    const appliedChanges = structuredClone(toRaw(state.changes));

    return commitChanges(this, context, appliedChanges);
  },

  /**
   * setError is a helper to handle errors from patching the app.
   */
  setError({ commit }, error: unknown) {
    let status: RDDClient.V1Status = {
      message: `${ error }`,
      details: {
        causes: [],
      },
    };
    if (error instanceof RDDClient.ApiException) {
      try {
        status = JSON.parse(error.body);
      } catch {
        // The error message is not JSON.
        status.message = `${ error.body }`;
      }
    }
    console.error('Failed to patch app:', JSON.stringify(status));
    commit('SET_ERROR_STATUS', status);
  },

  clear({ commit }) {
    commit('SET_CHANGES', {});
    commit('SET_ERROR_STATUS', undefined);
  },
} satisfies ActionTree<PreferencesState>;

/**
 * Helper function to generate a JSON patch from the given changes.
 * This ensures that the patch we submit for validation is the same as the patch
 * we submit for committing.
 * @note This is only exported for testing; it should not be used outside of
 * this module.
 */
export function generatePatch(app: App, changes: Changes): JSONPatch {
  const result: Record<string, JSONPatch[number]> = {};

  function escape(input: string) {
    return input.replaceAll('~', '~0').replaceAll('/', '~1');
  }

  for (const [key, value] of Object.entries(changes)) {
    const pathParts = ['', 'spec', ...key.split('.')];
    // We need to handle the case where parent objects are missing.  The index
    // is the number of elements, so it starts at 1, except we skip the root
    // because that will always exist.
    let missing = false;
    for (let i = 2; i < pathParts.length; ++i) {
      const subPath = pathParts.slice(0, i);
      if (_.get(app, subPath.slice(1), null) === null) {
        missing = true;
        if (value === undefined) {
          break;
        }
        const path = subPath.map(escape).join('/');
        result[path] = { op: 'add', path, value: {} };
      }
    }
    const path = pathParts.map(escape).join('/');
    if (value === undefined) {
      if (!missing && !(path in result)) {
        result[path] = { op: 'remove', path };
      }
    } else {
      // TODO: Handle array values.
      result[path] = { op: 'add', path, value };
    }
  }

  return Object.values(result);
}

enum PatchAppResult {
  /** There were no changes to apply. */
  skipped = 'skipped',
  /** The patch has failed; `commit('SET_ERROR_STATUS')` has been called. */
  failure = 'failure',
  /** Another request has replaced this one. */
  interrupted = 'interrupted',
}

/**
 * Record the given change in the state.
 */
function recordChange<K extends RecursiveLeafKeys<AppSpec>>(
  context: ActionContext<PreferencesState, typeof mutations>,
  { key, value }: { key: K, value: FieldType<AppSpec, K> },
) {
  const { commit, state, getters } = context;
  if (_.get(getters.committed, key) === value) {
    // Reverting a change; remove it from the changes list, so the UI does not
    // allow committing if all changes are being reverted.
    const { [key]: _unused, ...modified } = state.changes;
    commit('SET_CHANGES', modified);
  } else {
    commit('SET_CHANGES', { ...state.changes, [key]: value });
  }
}

/**
 * Helper function to patch the app with the given changes, possibly in dry-run.
 * This is used by `modify` and `commit`.
 * @return If successful, the generation of the updated app. Otherwise, a reason
 * the app was not patched.
 */
async function patchApp(
  context: ActionContext<PreferencesState, typeof mutations>,
  changes: Changes,
  serializer: SerializedRequestMiddleware,
  dryRun?: 'All',
): Promise<number | PatchAppResult> {
  const { commit, dispatch, rootState, rootGetters } = context;

  // Request validation of the merged preferences.
  const config = rootState['rdd-connection'].config;
  const client = config.makeApiClient(RDDClient.AppRancherdesktopIoV1alpha1Api);
  const app = rootGetters['rdd/app'];
  if (!app?.metadata?.name) {
    commit('SET_ERROR_STATUS', appMissingStatus);
    return PatchAppResult.failure;
  }

  if (Object.keys(changes).length === 0) {
    // No changes to validate or commit; clear any existing error status.
    serializer.abort();
    commit('SET_ERROR_STATUS', undefined);
    return PatchAppResult.skipped;
  }

  // Submit the changes as a JSON patch, so that we don't have to provide
  // values for things we don't care about that are marked as required.
  try {
    const result = await client.patchApp(
      {
        name:            app.metadata.name,
        body:            generatePatch(app, changes),
        fieldManager,
        fieldValidation: 'Strict',
        dryRun,
      },
      RDDClient.setHeaderOptions(
        'Content-Type',
        RDDClient.PatchStrategy.JsonPatch,
        { middleware: [serializer] }),
    );
    commit('SET_ERROR_STATUS', undefined);
    return result.metadata?.generation ?? 0;
  } catch (error) {
    if (Object.is(error, serializer.abortError)) {
      // This is a request that was aborted because a new request was made.  We
      // don't need to report this as an error.
      return PatchAppResult.interrupted;
    }
    await dispatch('setError', error);
    return PatchAppResult.failure;
  }
}

/**
 * Helper function to commit the given changes to the backend, and wait for the
 * app to be updated.  This is used by `writeNow` and `commit`.
 * @note Calling this will abort any in-flight validation requests, as well as
 *       any in-flight commit requests.
 * @returns Whether the commit was successful.
 */
async function commitChanges(
  store: Store<RootState>,
  context: ActionContext<PreferencesState, typeof mutations>,
  changes: Changes,
): Promise<boolean> {
  const { rootGetters } = context;
  pendingCommit.reset();

  // Clear any in-flight validation calls.
  modifySerializer.abort();

  let result: Awaited<ReturnType<typeof patchApp>> = PatchAppResult.failure;
  let skipResolve = false;

  try {
    result = await patchApp(context, changes, commitSerializer);

    // We update `state.changes` via the plugin below (by monitoring when
    // `app.spec` changes), so there is no need to do that here.

    // Check the result, and resolve `pendingCommit` if we are not interrupted.
    switch (result) {
    case PatchAppResult.skipped:
      return true;
    case PatchAppResult.failure:
      return false;
    case PatchAppResult.interrupted:
      // Don't resolve the pending commit; the replacement request will do that.
      skipResolve = true;
      return false;
    default: {
      if (typeof result !== 'number') {
        // Compile-time check that we handled all PatchAppResult cases.
        result satisfies never;
      }
      // The patch was successful; wait for `app.spec` to be updated.
      const latch = Latch();
      const timeout = setTimeout(() => {
        latch.resolve();
        unwatch();
      }, 1_000);
      const generation = result;
      const unwatch = store.watch(
        () => rootGetters['rdd/app']?.metadata?.generation ?? 0,
        (newGeneration) => {
          if (newGeneration >= generation) {
            clearTimeout(timeout);
            // Make sure `watch` returns before evaluating `unwatch`.
            queueMicrotask(() => unwatch());
            latch.resolve();
          }
        }, { immediate: true });
      await latch;
      return true;
    }
    }
  } finally {
    if (!skipResolve) {
      pendingCommit.resolve();
    }
  }
}

export const plugins: Plugin<RootState>[] = [
  // Whenever the app changes, check if any of the changes pending match the new
  // state, and clear any that do.
  function(store) {
    store.watch(
      (state, getters) => getters['rdd/app'],
      (app) => {
        if (!app?.spec) {
          // This could happen if the user deletes the app from under us.
          return;
        }
        const modified = structuredClone(toRaw(store.state.preferences.changes));
        let changed = false;
        for (const [key, value] of Object.entries(modified)) {
          if (_.get(app.spec, key) === value) {
            delete modified[key as keyof Changes];
            changed = true;
          }
        }
        if (changed) {
          store.commit('preferences/SET_CHANGES', modified);
        }
      },
      { deep: true },
    );
  },
];
