import { jest } from '@jest/globals';
import { mount } from '@vue/test-utils';
import { reactive } from 'vue';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { t } from '@pkg/utils/testUtils/translations';
import {
  IoRancherdesktopContainersV1alpha1Container as Container,
  IoRancherdesktopContainersV1alpha1ContainerStatusStatusEnum as ContainerStatus,
} from '@rdd-client';

const componentStub = { template: '<div />' };

/** Backs the mapped `container-engine` state; assign to it before mounting. */
const mockState = reactive<Record<string, any>>({});

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
      return Object.fromEntries(props.map((prop) => [prop, () => mockState[prop]]));
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

function mountContainers() {
  return mount(Containers, {
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
            'resource-fetch/manualRefreshIsLoading':     false,
          },
          commit:   jest.fn(),
          dispatch: jest.fn(),
        },
        t,
      },
      stubs: {
        T: { template: '<span></span>' },
      },
    },
  });
}

function makeContainer(name: string, composeProject?: string): Container {
  return {
    metadata: { name },
    status:   {
      image:     'scratch',
      namespace: 'default',
      name,
      path:      '/bin/false',
      status:    ContainerStatus.Running,
      labels:    composeProject ? { 'com.docker.compose.project': composeProject } : {},
    },
  };
}

describe('Containers methods', () => {
  it('adds restart actions for running containers', () => {
    const wrapper = mountContainers();
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
        label:    t('containers.manage.table.action.restart'),
        bulkable: true,
        enabled:  true,
      }),
    ]));
    expect(wrapper.vm.getContainerActions(stopped)).toEqual(expect.arrayContaining([
      expect.objectContaining({
        action:   'restartContainer',
        label:    t('containers.manage.table.action.restart'),
        bulkable: true,
        enabled:  false,
      }),
    ]));
  });
});

describe('Containers group selection', () => {
  const project = 'demo';

  beforeEach(() => {
    mockState.containers = [makeContainer('app', project), makeContainer('db', project)];
  });

  /** Wrap the rows the way SortableTable's group-row slot hands them over. */
  function groupOf(rows: any[]) {
    return { ref: project, rows: rows.map((row) => ({ row })) };
  }

  it('reuses row objects across recomputes so the selection survives', () => {
    const wrapper = mountContainers();
    const before = wrapper.vm.rows;

    expect(before).toHaveLength(2);

    // Fresh container objects, as a daemon update would deliver them.
    mockState.containers = [makeContainer('app', project), makeContainer('db', project)];

    const after = wrapper.vm.rows;

    expect(after[0]).toBe(before[0]);
    expect(after[1]).toBe(before[1]);
  });

  it('drops cached rows for containers that are gone', () => {
    const wrapper = mountContainers();

    expect(Object.keys(wrapper.vm.rowCache).sort()).toEqual(['app', 'db']);

    mockState.containers = [makeContainer('app', project)];
    expect(wrapper.vm.rows).toHaveLength(1);
    expect(Object.keys(wrapper.vm.rowCache)).toEqual(['app']);
  });

  it('reports a group as selected only when every container in it is', () => {
    const wrapper = mountContainers();
    const rows = wrapper.vm.rows;
    const group = groupOf(rows);

    expect(wrapper.vm.isGroupSelected(group)).toBe(false);
    expect(wrapper.vm.isGroupIndeterminate(group)).toBe(false);

    wrapper.vm.selectedRows = [rows[0]];
    expect(wrapper.vm.isGroupSelected(group)).toBe(false);
    expect(wrapper.vm.isGroupIndeterminate(group)).toBe(true);

    wrapper.vm.selectedRows = [rows[0], rows[1]];
    expect(wrapper.vm.isGroupSelected(group)).toBe(true);
    expect(wrapper.vm.isGroupIndeterminate(group)).toBe(false);
  });

  it('adds and removes the whole group from the table selection', () => {
    const wrapper = mountContainers();
    const rows = wrapper.vm.rows;
    const group = groupOf(rows);
    const table: any = wrapper.vm.$refs.sortableTableRef;
    const update = jest.spyOn(table, 'update');

    wrapper.vm.setGroupSelected(group, true);
    expect(update).toHaveBeenCalledWith(rows, []);

    wrapper.vm.setGroupSelected(group, false);
    expect(update).toHaveBeenCalledWith([], rows);
  });
});
