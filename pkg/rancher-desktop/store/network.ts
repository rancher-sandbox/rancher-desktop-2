import type { RootState } from '@pkg/entry/store';
import { MutationsType } from '@pkg/store/ts-helpers';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';

import type { Plugin } from 'vuex';

type NetworkState = ReturnType<typeof state>;

export const state = () => ({
  offline: false,
});

export const mutations = {
  SET_OFFLINE(state, isOffline) {
    state.offline = isOffline;
  },
} satisfies MutationsType<NetworkState>;

export const plugins: Plugin<RootState>[] = [
  // When the network state changes, update the store.
  function(store) {
    const updateNetworkState = () => {
      store.commit('network/SET_OFFLINE', !navigator.onLine);
    };

    window.addEventListener('online', updateNetworkState);
    window.addEventListener('offline', updateNetworkState);

    ipcRenderer.on('update-network-status', (_event, status) => {
      store.commit('network/SET_OFFLINE', !status);
    });

    // Set the initial state.
    updateNetworkState();
  },
];
