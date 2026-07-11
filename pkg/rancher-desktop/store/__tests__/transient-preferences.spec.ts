import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import _ from 'lodash';

import defaultTransientPreferences from '@pkg/types/transientPreferences';
import mockModules from '@pkg/utils/testUtils/mockModules';

const { '@pkg/utils/ipcRenderer': ipcRenderer } = mockModules({
  '@pkg/utils/ipcRenderer': {
    invoke: jest.fn((eventName: string, data: any) => Promise.resolve()),
    on:     jest.fn(),
    send:   jest.fn(),
  },
});

const { actions, mutations, plugins, state: stateFn } = await import('@pkg/store/transient-preferences');

describe('transient-preferences', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    // Jest does not implement structuredClone, so we need to do so for the tests.
    global.structuredClone ??= _.cloneDeep;
  });

  describe('state', () => {
    it('should return a structured clone of default transient preferences', () => {
      const s1 = stateFn();
      const s2 = stateFn();

      expect(s1).toEqual(defaultTransientPreferences);
      expect(s2).toEqual(defaultTransientPreferences);
      expect(s1).not.toBe(defaultTransientPreferences);
      expect(s2).not.toBe(defaultTransientPreferences);

      _.set(s1, 'navigation.__test.path', 'changed');
      expect(_.get(s2, 'navigation.__test.path')).toBeUndefined();
    });
  });

  describe('mutations', () => {
    it('SET_ALL should assign incoming partial preferences onto state', () => {
      const state = stateFn();

      state.navigation.preferences.application = 'environment';
      mutations.SET_ALL(state, { __test: { value: 123 } } as any);

      expect((state as any).__test.value).toBe(123);
      // It should not have removed existing keys.
      expect(state.navigation.preferences.application).toBe('environment');
    });

    it('navigate should set navigation keys using dot notation', () => {
      const state = stateFn();

      mutations.navigate(state, { '__test.path': 'bob' } as any);

      expect((state as any).navigation.__test.path).toBe('bob');
    });
  });

  describe('actions', () => {
    it('navigate should commit and persist via ipcRenderer', async() => {
      const state = stateFn();
      let committed = false;
      const commit = jest.fn((mutation: string, payload: any) => { committed = true });
      const navigation = { '__test.path': 'value' } as any;
      ipcRenderer.invoke.mockImplementation((eventName: string, data: any) => {
        expect(committed).toBe(true);
        return Promise.resolve();
      });

      await actions.navigate.call(null as any, { state, commit } as any, navigation);

      expect(commit).toHaveBeenCalledWith('navigate', navigation);
      expect(ipcRenderer.invoke).toHaveBeenCalledWith('transient-preferences/set', state);
    });
  });

  describe('plugins', () => {
    it('should register update listener and request initial state', () => {
      const store = { commit: jest.fn() };
      const plugin = plugins[0];

      plugin(store as any);

      expect(plugins).toHaveLength(1);
      expect(ipcRenderer.on).toHaveBeenCalledWith(
        'transient-preferences/update',
        expect.any(Function),
      );
      expect(ipcRenderer.send).toHaveBeenCalledWith('transient-preferences/get');
    });

    it('should commit SET_ALL when receiving transient-preferences/update', () => {
      const store = { commit: jest.fn() };
      const plugin = plugins[0];

      plugin(store as any);

      const callback = ipcRenderer.on.mock.calls[0][1] as (event: unknown, preferences: unknown) => void;
      const preferences = { navigation: { __test: { path: 1 } } };

      callback({}, preferences);

      expect(store.commit).toHaveBeenCalledWith('transient-preferences/SET_ALL', preferences);
    });
  });
});
