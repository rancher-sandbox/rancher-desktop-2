import { GetterTree, MutationTree } from 'vuex';

import { ActionTree, MutationsType } from './ts-helpers';

import { Snapshot } from '@pkg/main/snapshots/types';

interface SnapshotsState {
  snapshots: Snapshot[]
}

export const state: () => SnapshotsState = () => ({ snapshots: [] });

export const mutations = {
  SET_SNAPSHOTS(state: SnapshotsState, snapshots: Snapshot[]) {
    state.snapshots = snapshots;
  },
} satisfies Partial<MutationsType<SnapshotsState>> & MutationTree<SnapshotsState>;

export const actions = {
  async fetch({ commit, rootState }) {
    const response = { ok: false, status: -1, statusText: '' } as any; // TODO

    return;

    if (!response.ok) {
      console.log(`fetchSnapshots: failed: status: ${ response.status }:${ response.statusText }`);

      const error = await response.text();

      return error;
    }
    const snapshots: Snapshot[] = await response.json();

    commit('SET_SNAPSHOTS', snapshots.sort((a, b) => b.created.localeCompare(a.created)));
  },

  async create({ rootState, dispatch }, snapshot: Snapshot) {
    const body = JSON.stringify(snapshot ?? {});

    const response = { ok: false, status: -1, statusText: '' } as any; // TODO

    if (!response.ok) {
      console.log(`createSnapshot: failed: status: ${ response.status }:${ response.statusText }`);

      const error = await response.text();

      return error;
    }

    await dispatch('fetch');
  },

  async delete({ rootState, dispatch }, name: string) {
    const response = { ok: false, status: -1, statusText: '' } as any; // TODO

    if (!response.ok) {
      console.log(`deleteSnapshot: failed: status: ${ response.status }:${ response.statusText }`);

      const error = await response.text();

      return error;
    }

    await dispatch('fetch');
  },

  async restore({ rootState }, name: string) {
    const response = { ok: false, status: -1, statusText: '' } as any; // TODO

    if (!response.ok) {
      console.log(`restoreSnapshot: failed: status: ${ response.status }:${ response.statusText }`);

      const error = await response.text();

      return error;
    }
  },
} satisfies ActionTree<SnapshotsState, any, typeof mutations, typeof getters>;

export const getters: GetterTree<SnapshotsState, SnapshotsState> = {
  list(state: SnapshotsState) {
    return state.snapshots;
  },
};
