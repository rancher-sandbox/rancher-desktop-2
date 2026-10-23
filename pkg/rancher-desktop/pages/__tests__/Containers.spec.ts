import { jest } from '@jest/globals';
import { mount } from '@vue/test-utils';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { t } from '@pkg/utils/testUtils/translations';
import {
  IoRancherdesktopContainersV1alpha1Container as Container,
  IoRancherdesktopContainersV1alpha1ContainerStatusStatusEnum as ContainerStatus,
} from '@rdd-client';

const componentStub = { template: '<div />' };
const showMessageBox = jest.fn<(channel: string, options: any) => Promise<{ response: number }>>();

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
      invoke:         showMessageBox,
      removeListener: jest.fn(),
    },
  },
  '@pkg/components/BadgeState.vue':          componentStub,
  '@pkg/components/Banner.vue':              componentStub,
  '@pkg/components/form/Checkbox.vue':       componentStub,
  '@pkg/components/form/LabeledTooltip.vue': componentStub,
  electron:                                  { shell: { openExternal: jest.fn() } },
});

const { default: Containers } = await import('@pkg/pages/Containers.vue');

describe('Containers methods', () => {
  const { methods } = Containers as any;

  function container(name: string, labels: Record<string, string> = {}): Container {
    return {
      metadata: { name: `${ name }-id`, namespace: 'default' },
      status:   {
        image:     'scratch',
        namespace: 'default',
        name,
        path:      '/bin/false',
        status:    ContainerStatus.Exited,
        labels,
      },
    };
  }

  /** Build the row SortableTable would render for a single container. */
  function rowFor(item: Container, context: any) {
    const self = {
      containers:          [item],
      supportsNamespaces:  true,
      getContainerActions: () => [],
      getPortList:         () => [],
      ...context,
    };

    return (Containers as any).computed.rows.call(self)[0];
  }

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
          t,
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

  describe('grouping', () => {
    it.each([
      ['no grouping labels', {}, 'Standalone Containers'],
      ['a compose project label', { 'com.docker.compose.project': 'shop' }, 'shop'],
      ['kubernetes pod labels', {
        'io.kubernetes.pod.name':      'web-0',
        'io.kubernetes.pod.namespace': 'default',
      }, 'default/web-0'],
    ])('groups a container with %s', (_, labels, expectedGroup) => {
      expect(rowFor(container('web', labels), {}).projectGroup).toBe(expectedGroup);
    });
  });

  describe('group labels', () => {
    // Wrap the English text so a translated label is told apart from a raw group key.
    const marked = { t: (key: string) => `[${ t(key) }]` };

    it('translates the standalone group', () => {
      expect(methods.groupLabel.call(marked, 'Standalone Containers')).toBe('[Standalone Containers]');
    });

    it.each(['shop', 'default/web-0'])('shows the %s group name as is', (group) => {
      expect(methods.groupLabel.call(marked, group)).toBe(group);
    });
  });

  describe('deletion', () => {
    beforeEach(() => {
      showMessageBox.mockReset();
    });

    it('names and counts the containers in the confirmation', async() => {
      showMessageBox.mockResolvedValue({ response: 0 });

      await expect(methods.confirmDelete.call({ t }, [container('one'), container('two')])).resolves.toBe(true);
      expect(showMessageBox).toHaveBeenCalledWith('show-message-box', expect.objectContaining({
        message: 'Delete 2 containers?',
        detail:  'one\ntwo',
      }));
    });

    it('reports a cancelled confirmation, and defaults to Cancel', async() => {
      const cancelButton = 1;

      showMessageBox.mockResolvedValue({ response: cancelButton });
      await expect(methods.confirmDelete.call({ t }, [])).resolves.toBe(false);
      expect(showMessageBox).toHaveBeenCalledWith('show-message-box', expect.objectContaining({
        buttons:             ['&Delete', 'Cancel'],
        cancelId:            cancelButton,
        defaultId:           cancelButton,
        normalizeAccessKeys: true,
      }));
    });

    it('deletes the container once the user confirms', async() => {
      const containerDelete = jest.fn();
      const doomed = container('doomed');
      const row = rowFor(doomed, { containerDelete, confirmDelete: () => Promise.resolve(true) });

      await row.deleteContainer();
      expect(containerDelete).toHaveBeenCalledWith({ container: doomed });
    });

    it('confirms the whole bulk selection, and deletes none of it when cancelled', async() => {
      const containerDelete = jest.fn();
      const confirmDelete = jest.fn<(targets: Container[]) => Promise<boolean>>().mockResolvedValue(false);
      const doomed = [container('one'), container('two')];
      const row = rowFor(doomed[0], { containerDelete, confirmDelete });

      await row.deleteContainer(doomed);
      expect(confirmDelete).toHaveBeenCalledWith(doomed);
      expect(containerDelete).not.toHaveBeenCalled();
    });

    it('deletes the whole bulk selection once the user confirms', async() => {
      const containerDelete = jest.fn();
      const doomed = [container('one'), container('two')];
      const row = rowFor(doomed[0], { containerDelete, confirmDelete: () => Promise.resolve(true) });

      await row.deleteContainer(doomed);
      expect(containerDelete.mock.calls).toEqual(doomed.map(target => [{ container: target }]));
    });

    it('leaves the container alone when the user cancels', async() => {
      const containerDelete = jest.fn();
      const row = rowFor(container('spared'), { containerDelete, confirmDelete: () => Promise.resolve(false) });

      await row.deleteContainer();
      expect(containerDelete).not.toHaveBeenCalled();
    });
  });
});
