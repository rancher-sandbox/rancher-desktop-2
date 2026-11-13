import { jest } from '@jest/globals';
import { markRaw } from 'vue';
import { createStore } from 'vuex';

import mockModules from '@pkg/utils/testUtils/mockModules';

const send = jest.fn();

mockModules({
  '@pkg/entry/store':       undefined,
  electron:                 undefined,
  '@pkg/utils/ipcRenderer': { send },
});

const rdd = await import('../rdd');

const app = {
  apiVersion: 'app.rancherdesktop.io/v1alpha1',
  kind:       'App',
  metadata:   { name: 'app', resourceVersion: '1' },
  status:     { conditions: [{ type: 'KubernetesReady', status: 'True', reason: 'Ready' }] },
};

function appList(items: any[]) {
  return { metadata: { resourceVersion: '1' }, items };
}

/**
 * Build a store with the real rdd module and a fake rdd client.  The test
 * resolves each `listApp` call through `lists`, and sends watch events
 * through `watches`.
 */
function makeStore() {
  const lists: PromiseWithResolvers<any>[] = [];
  const watches: { callback: (phase: string, obj: any) => void, done: (err: any) => void }[] = [];
  const listApp = () => {
    const list = Promise.withResolvers<any>();

    lists.push(list);

    return list.promise;
  };
  const client = {
    listApp,
    listNamespace:           () => new Promise(() => {}),
    listNamespacedConfigMap: () => new Promise(() => {}),
    listHostInfo:            () => new Promise(() => {}),
  };
  const config = markRaw({ makeApiClient: () => client });
  const watch = markRaw({
    watch: (path: string, queryParams: any, callback: any, done: any) => {
      watches.push({ callback, done });

      return Promise.resolve(new AbortController());
    },
  });
  const store = createStore<any>({
    modules: {
      'rdd-connection': {
        namespaced: true,
        state:      () => ({ config, watch }),
        actions:    { fetchConfig: () => Promise.resolve(config) },
        mutations:  { SET_ERROR: () => {} },
      },
      rdd: {
        namespaced: true,
        state:      rdd.state,
        getters:    rdd.getters,
        mutations:  rdd.mutations,
        actions:    rdd.actions as any,
      },
    },
    plugins: rdd.plugins,
  });

  return { store, lists, watches };
}

describe('rdd store', () => {
  beforeEach(() => {
    jest.useFakeTimers();
    send.mockClear();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  async function watchApps() {
    const network = makeStore();

    await network.store.dispatch('rdd/watchResources', ['apps']);
    await jest.advanceTimersByTimeAsync(0);
    expect(network.lists).toHaveLength(1);

    return network;
  }

  it('does not send a status report before the App list returns', async() => {
    const { lists } = await watchApps();

    await jest.advanceTimersByTimeAsync(5_000);
    expect(send).not.toHaveBeenCalled();

    lists[0].resolve(appList([app]));
    await jest.advanceTimersByTimeAsync(0);
    expect(send.mock.calls).toEqual([
      ['backend/app-status-changed', { KubernetesReady: [true, 'Ready'] }],
    ]);
  });

  it('sends an empty status report when the first list does not have an App', async() => {
    const { lists } = await watchApps();

    lists[0].resolve(appList([]));
    await jest.advanceTimersByTimeAsync(0);
    expect(send.mock.calls).toEqual([['backend/app-status-changed', {}]]);
  });

  it('keeps the App while a restarted watch waits for a slow list', async() => {
    const { store, lists } = await watchApps();

    lists[0].resolve(appList([app]));
    await jest.advanceTimersByTimeAsync(0);
    send.mockClear();

    await store.dispatch('rdd/rewatchResources', ['apps']);
    await jest.advanceTimersByTimeAsync(5_000);
    expect(lists).toHaveLength(2);
    expect(store.state.rdd.apps).toEqual([app]);
    expect(send).not.toHaveBeenCalled();

    lists[1].resolve(appList([JSON.parse(JSON.stringify(app))]));
    await jest.advanceTimersByTimeAsync(0);
    expect(store.state.rdd.apps).toEqual([app]);
    expect(send.mock.calls).toEqual([
      ['backend/app-status-changed', { KubernetesReady: [true, 'Ready'] }],
    ]);
  });

  it('sends an empty status report when the list after a watch restart does not have an App', async() => {
    const { store, lists } = await watchApps();

    lists[0].resolve(appList([app]));
    await jest.advanceTimersByTimeAsync(0);
    send.mockClear();

    await store.dispatch('rdd/rewatchResources', ['apps']);
    lists[1].resolve(appList([]));
    await jest.advanceTimersByTimeAsync(0);
    expect(store.state.rdd.apps).toEqual([]);
    expect(send.mock.calls).toEqual([['backend/app-status-changed', {}]]);
  });

  it('ignores a list that returns after its watch restarted', async() => {
    const { store, lists } = await watchApps();

    await store.dispatch('rdd/rewatchResources', ['apps']);
    lists[0].resolve(appList([app]));
    await jest.advanceTimersByTimeAsync(0);
    expect(store.state.rdd.apps).toBeNull();
    expect(send).not.toHaveBeenCalled();

    lists[1].resolve(appList([app]));
    await jest.advanceTimersByTimeAsync(0);
    expect(send.mock.calls).toEqual([
      ['backend/app-status-changed', { KubernetesReady: [true, 'Ready'] }],
    ]);
  });

  it('commits an empty list and resolves waitForResources', async() => {
    const { store, lists } = await watchApps();
    let loaded = false;

    store.dispatch('rdd/waitForResources', ['apps']).then(() => {
      loaded = true;
    });
    expect(store.state.rdd.apps).toBeNull();

    lists[0].resolve(appList([]));
    await jest.advanceTimersByTimeAsync(0);
    expect(store.state.rdd.apps).toEqual([]);
    expect(loaded).toBe(true);
  });

  it('sends an empty status report when the App is deleted', async() => {
    const { lists, watches } = await watchApps();

    lists[0].resolve(appList([app]));
    await jest.advanceTimersByTimeAsync(0);
    expect(watches).toHaveLength(1);
    send.mockClear();

    watches[0].callback('DELETED', app);
    await jest.advanceTimersByTimeAsync(0);
    expect(send.mock.calls).toEqual([['backend/app-status-changed', {}]]);
  });
});
