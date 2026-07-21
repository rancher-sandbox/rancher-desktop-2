import { describe, jest, it, expect } from '@jest/globals';
import _ from 'lodash';

import { actions as rawActions, appMissingStatus, generatePatch, plugins, state as stateFn } from '@pkg/store/preferences';
import { ActionContext } from '@pkg/store/ts-helpers';
import Latch from '@pkg/utils/latch';
import * as RDDClient from '@rdd-client';
import { AppRancherdesktopIoV1alpha1Api as Api, IoRancherdesktopAppV1alpha1App as App } from '@rdd-client';

import type { Dispatch } from 'vuex';

let oldStructuredClone: { has: boolean, value: any };
beforeEach(() => {
  oldStructuredClone = { has: 'structuredClone' in global, value: global.structuredClone };
  global.structuredClone ??= (obj: any) => _.cloneDeep(obj);
});
afterEach(() => {
  if (oldStructuredClone.has) {
    global.structuredClone = oldStructuredClone.value;
  } else {
    delete (global as any).structuredClone;
  }
});

describe('actions', () => {
  const commit = jest.fn(function(this: ReturnType<typeof makeContext>, type: string, payload: any) {
    if (type === 'SET_CHANGES') {
      this.state.changes = payload;
    }
  });
  const dispatch = jest.fn<Dispatch>(() => Promise.resolve());
  const client = jest.mocked({ patchApp: jest.fn() } as unknown as Api);
  const rootState = {
    'rdd-connection': {
      config: {
        makeApiClient: jest.fn((arg: any) => {
          expect(arg).toBe(Api);
          return client;
        }),
      } as unknown as RDDClient.KubeConfig,
    },
  };
  const app = { spec: { running: false } } as unknown as App;
  const rootGetters = { 'rdd/app': app };
  const getters = {
    get committed() {
      return app.spec;
    },
  };
  // Convert `actions` to take anything as `this`, so we can avoid contortions
  // when calling them.
  const actions = Object.fromEntries(Object.entries(rawActions).map(
    ([k, v]) => [k, v.bind({ watch: jest.fn(() => jest.fn()) } as any)])) as {
    [K in keyof typeof rawActions]: typeof rawActions[K] extends (...args: any) => any ?
      (this: any, ...args: Parameters<typeof rawActions[K]>) => ReturnType<typeof rawActions[K]> : typeof rawActions[K];
  };

  function makeContext(options: { [ K in keyof ActionContext<any>]?: any } = {}): any {
    const result = {
      state:       options.state ?? stateFn(),
      getters:     options.getters ?? getters,
      commit:      options.commit ?? commit,
      dispatch:    options.dispatch ?? dispatch,
      rootState:   options.rootState ?? rootState,
      rootGetters: options.rootGetters ?? rootGetters,
    };
    result.commit = commit.bind(result);
    return result;
  }

  beforeEach(() => {
    jest.clearAllMocks();
    _.merge(app, { metadata: { name: 'app' }, spec: { running: false } });
    rootGetters['rdd/app'] = app;
  });

  interface mockRequestContext {
    signal:         AbortSignal | undefined;
    setHeaderParam: jest.Mock;
    setSignal:      jest.Mock;
  }
  /**
   * Create an API client; the callback will be called when `patchApp` is
   * called, and `patchApp` will only return when the result resolves.
   */
  function makeClient(callback: (requestContext: mockRequestContext) => Promise<void>) {
    let requestContext: mockRequestContext = {
      signal:         undefined,
      setHeaderParam: jest.fn(),
      setSignal:      jest.fn((signal: AbortSignal) => {
        requestContext.signal = signal;
      }),
    } as any;
    const api = {
      patchApp: jest.fn(async(app: any, options: RDDClient.ConfigurationOptions) => {
        for (const m of options.middleware ?? []) {
          requestContext = await m.pre(requestContext as any).toPromise() as any;
        }
        await callback(requestContext);
        for (const m of options.middleware ?? []) {
          requestContext = await m.post(requestContext as any).toPromise() as any;
        }
        return app;
      }),
    } as unknown as Api;
    return [api, requestContext] as const;
  }

  describe('modify', () => {
    it('should update preferences and changes', async() => {
      let context = makeContext();
      let newChange = { key: 'kubernetes.version', value: 'yes' as string } as const;
      let expectedChanges = { ...context.state.changes, [newChange.key]: newChange.value };

      // This ignores setting the error; that will be tested elsewhere.
      await actions.modify(context, newChange);

      expect(commit).toHaveBeenCalledWith('SET_CHANGES', expectedChanges);

      context = makeContext({ state: { namespace: 'laputa' } });

      newChange = { key: 'kubernetes.version', value: 'maybe' } as const;
      expectedChanges = { ...context.state.changes, [newChange.key]: newChange.value };
      await actions.modify(context, newChange);

      expect(commit).toHaveBeenCalledWith('SET_CHANGES', expectedChanges);
    });

    it('should remove change if value is reverted to committed state', async() => {
      // The committed state has kubernetes.version=hello
      _.set(app, 'spec.kubernetes.version', 'hello');
      const context = makeContext();

      // The changes has kubernetes.version=maybe queued.
      context.state.changes = { 'kubernetes.version': 'maybe' };

      // Set kubernetes.version=hello again, so the changes should be erased.
      await actions.modify(context, { key: 'kubernetes.version', value: 'hello' });
      expect(commit).toHaveBeenCalledWith('SET_CHANGES', {});
      // Because the was no change, `patchApp` should not have been called.
      expect(client.patchApp).not.toHaveBeenCalled();
    });

    it('should set error if app is missing', async() => {
      const context = makeContext({ rootGetters: { 'rdd/app': undefined } });

      await actions.modify(context, { key: 'running', value: true });

      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', appMissingStatus);
      expect(client.patchApp).not.toHaveBeenCalled();
    });

    it('should set error if patch fails', async() => {
      const context = makeContext();
      const error = new Error('error from unit test');
      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default' } };
      client.patchApp.mockRejectedValue(error);

      await actions.modify(context, { key: 'running', value: true });

      expect(client.patchApp).toHaveBeenCalledWith(
        expect.objectContaining({ body: generatePatch(context.state.changes) }),
        expect.anything());
      expect(dispatch).toHaveBeenCalledWith('setError', error);
    });

    it('should clear error if patch succeeds', async() => {
      const context = makeContext();
      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default' } };
      client.patchApp.mockResolvedValue(app);

      await actions.modify(context, { key: 'running', value: true });

      expect(client.patchApp).toHaveBeenCalledWith(
        expect.objectContaining({ body: generatePatch(context.state.changes) }),
        expect.anything());
      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', undefined);
    });

    it('should abandon patch if a new modify is called before the previous one completes', async() => {
      // The flow to test the abort is:
      // - A request is made; its middleware pre() hooks are run (to set up the signal)
      // - That request is then suspended
      // - A second request is made, running the same middleware hooks and aborting the first request
      // - The second request runs to completion.
      // - The first request resumes, and is aborted from the signal.
      const requestCompleted = Latch<void>();
      const middlewarePreCompleted = Latch<void>();
      const [stuckClient, stuckRequestContext] = makeClient(async(requestContext) => {
        expect(requestContext.setSignal).toHaveBeenCalled();
        middlewarePreCompleted.resolve();
        await requestCompleted;
        expect(requestContext.signal).toHaveProperty('aborted', true);
        requestContext.signal?.throwIfAborted();
      });
      const [fastClient, _requestContext] = makeClient((requestContext) => {
        expect(requestContext.setSignal).toHaveBeenCalled();
        return Promise.resolve();
      });
      const context = makeContext();
      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default' } };
      context.state.changes = { 'kubernetes.version': 'one' };

      jest.mocked(rootState['rdd-connection'].config.makeApiClient).mockReturnValueOnce(stuckClient);
      const first = actions.modify(context, { key: 'kubernetes.version', value: 'two' });
      await middlewarePreCompleted;

      jest.mocked(rootState['rdd-connection'].config.makeApiClient).mockReturnValueOnce(fastClient);
      await actions.modify(context, { key: 'kubernetes.version', value: 'three' });
      requestCompleted.resolve(); // Let the first request complete, but it should be abandoned.
      await first;

      expect(fastClient.patchApp).toHaveBeenCalled();
      expect(stuckRequestContext.setSignal).toHaveBeenCalled();
      expect(dispatch).not.toHaveBeenCalledWith('setError', expect.anything());
      expect(stuckRequestContext.signal?.aborted).toBe(true);
    });

    it('should skip validation if there are no changes', async() => {
      // If one request changess something, and then a second request reverts it
      // before the first request completes, we should pass validation.
      const context = makeContext();
      _.set(app, 'spec.running', false); // Ensure the committed state.
      // Make a request, but do not let it complete.
      const promise = actions.modify(context, { key: 'running', value: true });
      // Revert the change.
      await actions.modify(context, { key: 'running', value: false });
      await promise;
    });
  });

  describe('commit', () => {
    it('should set error if app is missing', async() => {
      rootGetters['rdd/app'] = undefined as any;
      const context = makeContext();
      const result = await actions.commit.call(null as any, context);

      expect(result).toBe(false);
      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', appMissingStatus);
      expect(client.patchApp).not.toHaveBeenCalled();
    });

    it('should do nothing if there are no changes', async() => {
      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default' } };
      const context = makeContext();
      context.state.changes = {};
      const result = await actions.commit.call(null as any, context);

      expect(result).toBe(true);
      expect(client.patchApp).not.toHaveBeenCalled();
      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', undefined);
      expect(commit).not.toHaveBeenCalledWith('SET_CHANGES', expect.anything());
    });

    it('should set error if patch fails', async() => {
      const context = makeContext({ state: _.merge(stateFn(), { changes: { running: true } }) });
      const error = new Error('error from unit test');
      client.patchApp.mockRejectedValue(error);

      const body = generatePatch(context.state.changes);
      const result = await actions.commit(context);

      expect(result).toBe(false);
      // Should not call `commit` directly, but go through `dispatch('setError')`.
      expect(commit).not.toHaveBeenCalledWith('SET_ERROR_STATUS', expect.anything());
      // On failure, it should not clear changes that were not applied.
      expect(commit).not.toHaveBeenCalledWith('SET_CHANGES', expect.anything());
      expect(client.patchApp).toHaveBeenCalledWith(expect.objectContaining({ body }), expect.anything());
      expect(dispatch).toHaveBeenCalledWith('setError', error);
    });

    it('should clear error if patch succeeds', async() => {
      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default' } };
      const state = _.merge(stateFn(), { changes: { running: true, 'kubernetes.enabled': undefined, 'invalid/name~': 1 } });
      const context = makeContext({ state });
      client.patchApp.mockResolvedValue(app);

      // Save the expected body before `commit` modifies `state.changes`.
      const body = generatePatch(context.state.changes);

      // We use a custom `watch` to call the callback immediately.
      const watch = jest.fn((getter: () => number, callback: (value: number) => void) => {
        callback(getter());
        return jest.fn();
      });
      const result = await rawActions.commit.call({ watch } as any, context);

      expect(result).toBe(true);
      expect(client.patchApp).toHaveBeenCalledWith(
        expect.objectContaining({ body }), expect.anything());
      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', undefined);
    });

    it('should wait for the generation to update', async() => {
      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default', generation: 1 } };
      const context = makeContext({ state: _.merge(stateFn(), { changes: { running: true } }) });
      const resultApp = { metadata: { name: 'test', generation: 3 } };

      client.patchApp.mockResolvedValue(resultApp);

      let watchCallback: (gen: number) => void = () => { };
      const unwatch = jest.fn();
      const watch = jest.fn((getter: () => number, callback: (gen: number) => void, options: any) => {
        expect(getter()).toBe(1);
        expect(options).toMatchObject({ immediate: true });
        watchCallback = callback;
        return unwatch;
      });
      const commitPromise = rawActions.commit.call({ watch } as any, context);

      // Wait for the watch to be set up.
      await new Promise(process.nextTick);
      expect(client.patchApp).toHaveBeenCalled();
      expect(watch).toHaveBeenCalled();
      expect(unwatch).not.toHaveBeenCalled();

      // Check that an early callback does not resolve the promise.
      _.set(rootGetters['rdd/app'], 'metadata.generation', resultApp.metadata.generation - 1);
      watchCallback(resultApp.metadata.generation - 1);
      expect(unwatch).not.toHaveBeenCalled();

      // Simulate the generation updating.
      _.set(rootGetters['rdd/app'], 'metadata.generation', resultApp.metadata.generation);
      watchCallback(resultApp.metadata.generation);

      const result = await commitPromise;

      expect(result).toBe(true);
      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', undefined);
      expect(unwatch).toHaveBeenCalled();
    });

    it('should abandon a previous commit if a new commit is started', async() => {
      const requestCompleted = Latch<void>();
      const middlewarePreCompleted = Latch<void>();

      const [stuckClient, stuckRequestContext] = makeClient(async(requestContext) => {
        expect(requestContext.setSignal).toHaveBeenCalled();
        middlewarePreCompleted.resolve();
        await requestCompleted;
        expect(requestContext.signal).toHaveProperty('aborted', true);
        requestContext.signal?.throwIfAborted();
      });
      const [fastClient, _requestContext] = makeClient((requestContext) => {
        expect(requestContext.setSignal).toHaveBeenCalled();
        return Promise.resolve();
      });

      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default', generation: 0 } };
      const context = makeContext({ state: _.merge(stateFn(), { changes: { running: true } }) });

      const watch = jest.fn((getter: () => number, callback: (value: number) => void) => {
        callback(getter());
        return jest.fn();
      });

      jest.mocked(rootState['rdd-connection'].config.makeApiClient).mockReturnValueOnce(stuckClient);
      const first = rawActions.commit.call({ watch } as any, context);
      await middlewarePreCompleted;

      jest.mocked(rootState['rdd-connection'].config.makeApiClient).mockReturnValueOnce(fastClient);
      const second = rawActions.commit.call({ watch } as any, context);

      requestCompleted.resolve();

      const [firstResult, secondResult] = await Promise.all([first, second]);

      expect(firstResult).toBe(false);
      expect(secondResult).toBe(true);
      expect(fastClient.patchApp).toHaveBeenCalled();
      expect(stuckRequestContext.setSignal).toHaveBeenCalled();
      expect(stuckRequestContext.signal?.aborted).toBe(true);
      expect(dispatch).not.toHaveBeenCalledWith('setError', expect.anything());
    });
  });

  describe('writeNow', () => {
    it('should patch only the requested key and return true on success', async() => {
      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default', generation: 0 } };
      const context = makeContext({ state: _.merge(stateFn(), { changes: { 'kubernetes.version': 'v1.2.3' } }) });
      client.patchApp.mockResolvedValue({ metadata: { generation: 0 } });

      const watch = jest.fn((getter: () => number, callback: (value: number) => void) => {
        callback(getter());
        return jest.fn();
      });

      const result = await rawActions.writeNow.call({ watch } as any, context, { key: 'running', value: true });

      expect(result).toBe(true);
      expect(client.patchApp).toHaveBeenCalledWith(
        expect.objectContaining({ body: generatePatch({ running: true }) }),
        expect.anything(),
      );
      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', undefined);
    });

    it('should roll back only the failed key when patch fails', async() => {
      rootGetters['rdd/app'] = { metadata: { name: 'test', namespace: 'default', generation: 0 } };
      const context = makeContext({ state: _.merge(stateFn(), { changes: { 'kubernetes.version': 'v1.2.3' } }) });
      const error = new Error('error from unit test');
      client.patchApp.mockRejectedValue(error);

      const watch = jest.fn((getter: () => number, callback: (value: number) => void) => {
        callback(getter());
        return jest.fn();
      });

      const result = await rawActions.writeNow.call({ watch } as any, context, { key: 'running', value: true });

      expect(result).toBe(false);
      expect(client.patchApp).toHaveBeenCalledWith(
        expect.objectContaining({ body: generatePatch({ running: true }) }),
        expect.anything(),
      );
      expect(dispatch).toHaveBeenCalledWith('setError', error);
      expect(commit).toHaveBeenCalledWith('SET_CHANGES', { 'kubernetes.version': 'v1.2.3' });
    });

    it('should set missing-app error and roll back local change if app is missing', async() => {
      rootGetters['rdd/app'] = undefined as any;
      const context = makeContext({ state: _.merge(stateFn(), { changes: { 'kubernetes.version': 'v1.2.3' } }) });

      const watch = jest.fn((getter: () => number, callback: (value: number) => void) => {
        callback(getter());
        return jest.fn();
      });

      const result = await rawActions.writeNow.call({ watch } as any, context, { key: 'running', value: true });

      expect(result).toBe(false);
      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', appMissingStatus);
      expect(client.patchApp).not.toHaveBeenCalled();
      expect(commit).toHaveBeenCalledWith('SET_CHANGES', { 'kubernetes.version': 'v1.2.3' });
    });
  });

  describe('setError', () => {
    // `setError` also logs the status to the console; we don't care about testing that.
    beforeAll(() => {
      jest.spyOn(global.console, 'error').mockImplementation(() => { /* ignore */ });
    });
    afterAll(() => {
      jest.mocked(global.console.error).mockRestore();
    });

    it('should use message for unknown error', () => {
      const context = makeContext();
      const error = new Error('error from unit test');

      actions.setError(context, error);

      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', expect.objectContaining({
        message: 'Error: error from unit test',
      }));
    });

    it('should use status from ApiException', () => {
      const context: any = { commit };
      const status: RDDClient.V1Status = {
        message: 'error from unit test',
        details: {
          causes: [],
        },
      };
      const error = new RDDClient.ApiException(500, 'error from unit test', JSON.stringify(status), {});

      actions.setError(context, error);

      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', status);
    });

    it('should fall back to message if ApiException body is not JSON', () => {
      const context: any = { commit };
      const error = new RDDClient.ApiException(500, 'error from unit test', 'not JSON', {});

      actions.setError(context, error);

      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', expect.objectContaining({
        message: 'not JSON',
      }));
    });
  });

  describe('clear', () => {
    it('should clear changes and error', () => {
      rootGetters['rdd/app'] = { spec: { running: false } };
      const context = makeContext();

      actions.clear(context);

      expect(commit).toHaveBeenCalledWith('SET_CHANGES', {});
      expect(commit).toHaveBeenCalledWith('SET_ERROR_STATUS', undefined);
    });
  });
});

describe('generatePatch', () => {
  it('should generate a JSON patch from changes', () => {
    const changes = { running: true, 'kubernetes.enabled': undefined, 'invalid/name~': 1 };
    const patch = generatePatch(changes);

    expect(patch).toEqual([
      { op: 'add', path: '/spec/running', value: true },
      { op: 'remove', path: '/spec/kubernetes/enabled' },
      { op: 'add', path: '/spec/invalid~1name~0', value: 1 },
    ]);
  });
});

describe('plugins', () => {
  function testWatchCallback(testImpl: (callback: (app: any) => any, app: App, changesGetter: jest.Mock, store: any) => any) {
    const app = { spec: { running: false } } as App;
    const getters = { 'rdd/app': app };
    const changesGetter = jest.fn().mockReturnValue({});
    const store = {
      state:  { preferences: { } },
      commit: jest.fn(),
      watch:  jest.fn(
        (getter: (state: any, getters: any) => any, callback: (app: any) => any, options: any) => {
          expect(getter({}, getters)).toBe(app);

          testImpl(callback, app, changesGetter, store);
        },
      ),
    };
    Object.defineProperty(store.state.preferences, 'changes', { get: changesGetter });
    const plugin = plugins[0];
    plugin(store as any);

    expect(plugins).toHaveLength(1);
    expect(store.watch).toHaveBeenCalledWith(
      expect.any(Function),
      expect.any(Function),
      { deep: true },
    );
  }

  it('should do nothing if the app is undefined', () => {
    testWatchCallback((callback, _app, changesGetter, store) => {
      expect(() => callback(undefined)).not.toThrow();
      expect(changesGetter).not.toHaveBeenCalled();
      expect(store.commit).not.toHaveBeenCalled();
    });
  });

  it('should do nothing if the app has no spec', () => {
    testWatchCallback((callback, app, changesGetter, store) => {
      app.spec = undefined;
      expect(() => callback(app)).not.toThrow();
      expect(changesGetter).not.toHaveBeenCalled();
      expect(store.commit).not.toHaveBeenCalled();
    });
  });

  it('should clear applied settings', () => {
    testWatchCallback((callback, app, changesGetter, store) => {
      changesGetter.mockReturnValue({ running: false, 'kubernetes.version': 'eclair' });
      expect(() => callback(app)).not.toThrow();
      expect(changesGetter).toHaveBeenCalled();
      expect(store.commit).toHaveBeenCalledWith(
        'preferences/SET_CHANGES', { 'kubernetes.version': 'eclair' });
    });
  });
});
