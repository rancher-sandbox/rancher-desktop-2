import { jest } from '@jest/globals';
import { createStore } from 'vuex';

import { ActionContext } from '@pkg/store/ts-helpers';
import mockModules from '@pkg/utils/testUtils/mockModules';

import type { state as StateFunc } from '../container-engine';
import type { HttpLibrary } from '@rdd-client';

type State = ReturnType<typeof StateFunc>;

mockModules({
  '@pkg/entry/store': undefined,
  electron:           undefined,
});

const containerEngineModule = await import('../container-engine');
const { actions: realActions } = containerEngineModule;
const { ApiException, createConfiguration, Observable, ResponseContext, ServerConfiguration } = await import('@rdd-client');

function of<T>(value: T) {
  return new Observable<T>(Promise.resolve(value));
}

type actionContextType = ActionContext<State,
  typeof containerEngineModule.mutations,
  typeof containerEngineModule.getters>;

describe('container-engine store', () => {
  describe('getters.currentNamespace', () => {
    const { currentNamespace } = containerEngineModule.getters;
    it('should return undefined when namespace are not supported', () => {
      const getters = { supportsNamespaces: false };
      expect(currentNamespace({} as State, getters)).toBeUndefined();
    });
    it('should return selected namespace when available', () => {
      const getters = {
        supportsNamespaces: true,
        namespaces:         ['three-per-em'],
      };
      const state = { selectedNamespace: 'three-per-em' } as unknown as State;
      expect(currentNamespace(state, getters)).toBe('three-per-em');
    });
    it('should return default as a fallback if available', () => {
      const getters = {
        supportsNamespaces: true,
        namespaces:         ['figure', 'default', 'hair'],
      };
      const state = { } as unknown as State;
      expect(currentNamespace(state, getters)).toBe('default');
    });
    it('should return fallback if currently selected namespace goes away', () => {
      const getters = {
        supportsNamespaces: true,
        namespaces:         ['figure', 'default', 'hair'],
      };
      const state = { selectedNamespace: 'four-per-em' } as unknown as State;
      expect(currentNamespace(state, getters)).toBe('default');
    });
    it('should pick the first one if no default is available', () => {
      const getters = {
        supportsNamespaces: true,
        namespaces:         ['punctuation', 'thin'],
      };
      const state = { } as unknown as State;
      expect(currentNamespace(state, getters)).toBe('punctuation');
    });
    it('should return undefined if no namespaces exist', () => {
      const getters = {
        supportsNamespaces: true,
        namespaces:         [],
      };
      const state = { } as unknown as State;
      expect(currentNamespace(state, getters)).toBeUndefined();
    });
  });
  describe('actions.setCurrentNamespace', () => {
    const { setCurrentNamespace } = realActions;
    it('should ignore no-op updates', () => {
      const commit = jest.fn();
      const state = { selectedNamespace: 'six-per-em' } as unknown as State;
      const context = { commit, state } as unknown as actionContextType;
      setCurrentNamespace.call(null as any, context, { namespace: 'six-per-em' });
      expect(commit).not.toHaveBeenCalled();
    });
    it('should error if namespaces are not supported', () => {
      const commit = jest.fn();
      const state = { selectedNamespace: 'thin' } as unknown as State;
      const getters = { supportsNamespaces: false };
      const context = { commit, state, getters } as unknown as actionContextType;
      jest.spyOn(console, 'log').mockImplementation(() => {});
      setCurrentNamespace.call(null as any, context, { namespace: 'hair' });
      expect(commit).toHaveBeenCalledWith('SET_ERROR', expect.objectContaining({
        error:  expect.any(Error),
        source: 'namespaces',
      }));
    });
    it('should error if new namespace does not exist', () => {
      const commit = jest.fn();
      const state = { selectedNamespace: 'en' } as unknown as State;
      const getters = { supportsNamespaces: true, namespaces: ['medium-mathematical'] };
      const context = { commit, state, getters } as unknown as actionContextType;
      jest.spyOn(console, 'log').mockImplementation(() => {});
      setCurrentNamespace.call(null as any, context, { namespace: 'zero-width' });
      expect(commit).toHaveBeenCalledWith('SET_ERROR', expect.objectContaining({
        error:  expect.any(Error),
        source: 'namespaces',
      }));
    });
    it('should set the selected namespace', () => {
      const commit = jest.fn();
      const state = { selectedNamespace: 'no-break' } as unknown as State;
      const getters = { supportsNamespaces: true, namespaces: ['narrow-no-break'] };
      const dispatch = jest.fn();
      const context = { commit, state, getters, dispatch } as unknown as actionContextType;
      setCurrentNamespace.call(null as any, context, { namespace: 'narrow-no-break' });
      expect(commit).toHaveBeenCalledWith('SET_SELECTED_NAMESPACE', 'narrow-no-break');
    });
  });
  describe.each([
    ['containerDelete', 'Container', 'container', 'containers'],
    ['imageDelete', 'Image', 'image', 'images'],
    ['volumeDelete', 'Volume', 'volume', 'volumes'],
  ] as const)('actions.%s', (action, kind, argument, source) => {
    /**
     * Run the action against a *real* API client, mocking only the raw HTTP
     * transport (`HttpLibrary.send`) so that the real request building,
     * response processing, and `ObjectSerializer` deserialization all run as
     * they would in production.
     */
    async function deleteResolving(status: number, responseBody: object | string) {
      const commit = jest.fn();
      const text = typeof responseBody === 'string' ? responseBody : JSON.stringify(responseBody);
      const httpApi: HttpLibrary = {
        send: () => of(new ResponseContext(status, { 'content-type': 'application/json' }, {
          text:   () => Promise.resolve(text),
          binary: () => Promise.resolve(new Blob([text])),
        })),
      };
      const apiConfig = createConfiguration({ baseServer: new ServerConfiguration('http://example.test', {}), httpApi });
      const config = { makeApiClient: (Api: any) => new Api(apiConfig) };
      const context = { commit, rootState: { 'rdd-connection': { config } } } as unknown as actionContextType;
      const resource = { metadata: { name: 'gone', namespace: 'default' } };

      await (realActions[action] as any)(context, { [argument]: resource });

      return commit;
    }

    it('should treat a resource that no longer exists as deleted', async() => {
      const commit = await deleteResolving(404,
        { kind: 'Status', status: 'Failure', reason: 'NotFound', code: 404 });

      expect(commit).not.toHaveBeenCalled();
    });

    it('should accept returning a deletion status', async() => {
      const commit = await deleteResolving(200, { kind: 'Status', status: 'Success' });

      expect(commit).not.toHaveBeenCalled();
    });

    it('should accept returning a deletion status with no kind', async() => {
      const commit = await deleteResolving(200, { status: 'Success' });

      expect(commit).not.toHaveBeenCalled();
    });

    it('should accept returning the object with deletion timestamp', async() => {
      const commit = await deleteResolving(200, {
        kind,
        metadata: { name: 'gone', namespace: 'default', deletionTimestamp: new Date().toISOString() },
      });

      expect(commit).not.toHaveBeenCalled();
    });

    it('should accept returning an object without deletion timestamp', async() => {
      const commit = await deleteResolving(200, {
        kind,
        metadata: { name: 'gone', namespace: 'default' },
      });

      expect(commit).not.toHaveBeenCalled();
    });

    it('should report any other failure', async() => {
      const commit = await deleteResolving(500, '');

      expect(commit).toHaveBeenCalledWith('SET_ERROR', {
        error:  expect.objectContaining({
          constructor: ApiException, code: 500, body: '',
        }),
        source,
      });
    });
  });
  describe('rewatchResourcesPlugin', () => {
    it('should rewatch resources on namespace change', async() => {
      // Disable the plugin that sets up resource watching automatically; we are
      // testing the other one.
      jest.spyOn(containerEngineModule.actions, 'setupResourceWatch')
        .mockResolvedValue(undefined);

      const store = createStore({
        modules: {
          'container-engine': { ...containerEngineModule, namespaced: true },
          rdd:                {
            namespaced: true,
            getters:    { app: () => ({ status: { supportsNamespaces: true } }) },
          },
        },
        plugins: containerEngineModule.plugins,
      });

      const dispatch = jest.spyOn(store, 'dispatch').mockResolvedValue(undefined);

      // Loading the namespace list should cause a refresh, because the default
      // namespace has changed (from undefined to _something_).
      store.commit('container-engine/SET_NAMESPACES', [{ status: { name: 'default' } }]);

      // Advance a few ticks to allow any asynchronous watchers to trigger.
      await new Promise<void>(queueMicrotask);

      expect(dispatch).toHaveBeenCalledWith('container-engine/rewatchResources',
        expect.arrayContaining(['containers']));
    });
  });
});
