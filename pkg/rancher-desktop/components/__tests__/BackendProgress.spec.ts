import { jest } from '@jest/globals';
import { shallowMount } from '@vue/test-utils';
import { createStore } from 'vuex';

import mockModules from '@pkg/utils/testUtils/mockModules';

mockModules({
  '@pkg/entry/store': {},
});

const { default: BackendProgress } = await import('../BackendProgress.vue');

describe('BackendProgress', () => {
  beforeAll(() => {
    jest.useFakeTimers();
  });

  function makeCondition(type: string, status: string, order: number) {
    return {
      type,
      status,
      message:            `${ type }/${ status }`,
      lastTransitionTime: new Date(`${ 2000 + order }-01-01T00:00:00Z`),
    };
  }
  function F(type: string, order = 0) {
    return makeCondition(type, 'False', order);
  }
  function T(type: string, order = 0) {
    return makeCondition(type, 'True', order);
  }

  const testCases = [
    // Test case name, expected text (or empty for no progress), and input conditions.
    ['No conditions', 'Starting control plane', []],
    ['Settled', '', [T('Settled'), F('Other')]],
    ['Get message from condition', 'A/False', [F('A', 0)]],
    ['Use most recent condition', 'B/False', [F('A', 1), F('B', 2), F('C', 0)]],
    ['Ignores true conditions', 'A/False', [F('A', 0), T('B', 1)]],
    ['Prefer Created over Running', 'Created/False', [F('Running'), F('Created')]],
    ['Prefer Running over ContainerEngineReady', 'Running/False', [F('ContainerEngineReady'), F('Running')]],
    ['Prefer ContainerEngineReady over KubernetesReady', 'ContainerEngineReady/False', [F('KubernetesReady'), F('ContainerEngineReady')]],
    ['Prefer KubernetesReady over Settled', 'KubernetesReady/False', [F('Settled'), F('KubernetesReady')]],
    ['Prefer Settled if present', 'Settled/False', [F('Unknown'), F('Settled')]],
  ];
  test.each(testCases)('%s', (name, expected, conditions) => {
    const store = createStore({
      getters: {
        'rdd/settled': () => !expected,
        'rdd/app':     () => ({ status: { conditions } }),
      },
    });

    const wrapper = shallowMount(BackendProgress, {
      global: { plugins: [store] },
    });
    if (expected) {
      expect(wrapper.text()).toContain(expected);
    } else {
      expect(wrapper.find('.progress').exists()).toBeFalsy();
    }
  });
});
