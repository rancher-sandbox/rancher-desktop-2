import { jest } from '@jest/globals';
import { mount } from '@vue/test-utils';

import mockModules from '@pkg/utils/testUtils/mockModules';
import {
  IoRancherdesktopContainersV1alpha1Container as Container,
  IoRancherdesktopContainersV1alpha1ContainerStatusStatusEnum as ContainerStatus,
} from '@rdd-client';

const componentStub = { template: '<div />' };

mockModules({
  '@pkg/entry/store':  {
    mapTypedActions(module: string, arg: string[] | Record<string, string>) {
      const actions: Record<string, Record<string, jest.Mock>> = {
        'container-engine': {
          watchResources:   jest.fn(() => Promise.resolve()),
          unwatchResources: jest.fn(() => Promise.resolve()),
        },
      };
      const props = Array.isArray(arg) ? arg : Object.values(arg);
      return Object.fromEntries(props.map((prop) => [
        prop,
        actions[module]?.[prop] ?? jest.fn(),
      ]));
    },
    mapTypedGetters(module: string, arg: string[] | Record<string, string>) {
      const props = Array.isArray(arg) ? arg : Object.values(arg);
      return Object.fromEntries(
        props.map((prop) => [prop, jest.fn()]));
    },
    mapTypedMutations(module: string, arg: string[] | Record<string, string>) {
      const props = Array.isArray(arg) ? arg : Object.values(arg);
      return Object.fromEntries(props.map((prop) => [prop, jest.fn()]));
    },
    mapTypedState(module: string, arg: string[] | Record<string, string>) {
      const props = Array.isArray(arg) ? arg : Object.values(arg);
      return Object.fromEntries(props.map((prop) => [prop, jest.fn()]));
    },
  },
  '@pkg/utils/ipcRenderer': {
    ipcRenderer: {
      on:             jest.fn(),
      send:           jest.fn(),
      invoke:         jest.fn(),
      removeListener: jest.fn(),
    },
  },
  '@rancher/components': {
    BadgeState:     componentStub,
    Banner:         componentStub,
    Checkbox:       componentStub,
    LabeledTooltip: componentStub,
  },
  electron: { shell: { openExternal: jest.fn() } },
});

const { default: Containers } = await import('@pkg/pages/Containers.vue');

describe('Containers methods', () => {
  it('adds restart actions for running containers', () => {
    const wrapper = mount(Containers, {
      global: {
        directives: {
          'clean-html':      {},
          'clean-tooltip':   {},
          'close-popper':    {},
          shortkey:          {},
          tooltip:           {},
          'trim-whitespace': {},
        },
        mocks: {
          $store: {
            getters:  {
              'resource-fetch/isTooManyItemsToAutoUpdate': false,
            },
            commit:   jest.fn(),
            dispatch: jest.fn(),
          },
          t: (s: string) => s,
        },
        stubs: {
          T: { template: '<span></span>' },
        },
      },
    });
    const running: Container = {
      status: {
        image:     'scratch',
        namespace: 'default',
        name:      'stopped-container',
        path:      '/bin/false',
        status:    ContainerStatus.Running,
      },
    };
    const stopped: Container = {
      status: {
        image:     'scratch',
        namespace: 'default',
        name:      'stopped-container',
        path:      '/bin/false',
        status:    ContainerStatus.Exited,
      },
    };

    expect(wrapper.vm.getContainerActions(running)).toEqual(expect.arrayContaining([
      expect.objectContaining({
        action:   'restartContainer',
        label:    'Restart',
        bulkable: true,
        enabled:  true,
      }),
    ]));
    expect(wrapper.vm.getContainerActions(stopped)).toEqual(expect.arrayContaining([
      expect.objectContaining({
        action:   'restartContainer',
        label:    'Restart',
        bulkable: true,
        enabled:  false,
      }),
    ]));
  });
});
