import { jest } from '@jest/globals';
import { mount } from '@vue/test-utils';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { sortableTableGlobal } from '@pkg/utils/testUtils/sortableTable';
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
const { default: SortableTable } = await import('@pkg/components/SortableTable/index.vue');

describe('Containers methods', () => {
  const { methods } = Containers as any;

  function container(name: string, labels: Record<string, string> = {}): Container {
    return {
      metadata: { name: `${ name }-id`, namespace: 'default' },
      status:   {
        id:        `${ name }-id`,
        image:     'scratch',
        namespace: 'default',
        name,
        path:      '/bin/false',
        status:    ContainerStatus.Exited,
        labels,
      },
    };
  }

  /** Build the rows SortableTable would render for the given containers. */
  function rowsFor(items: Container[], context: any) {
    const self = {
      containers:          items,
      supportsNamespaces:  true,
      getContainerActions: () => [],
      getPortList:         () => [],
      ...context,
    };

    return (Containers as any).computed.rows.call(self);
  }

  /** Build the row SortableTable would render for a single container. */
  function rowFor(item: Container, context: any) {
    return rowsFor([item], context)[0];
  }

  it('adds restart actions for running containers', () => {
    const wrapper = mount(Containers, { global: sortableTableGlobal() });
    const running: Container = {
      status: {
        id:        'running-id',
        image:     'scratch',
        namespace: 'default',
        name:      'stopped-container',
        path:      '/bin/false',
        status:    ContainerStatus.Running,
      },
    };
    const stopped: Container = {
      status: {
        id:        'stopped-id',
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

  describe('search', () => {
    it.each([
      ['name', 'web'],
      ['state', 'running'],
      ['port, as the column shows it', '8080:80'],
    ])('finds a container by its %s', (_, query) => {
      const web = container('web');

      web.status!.status = ContainerStatus.Running;
      web.status!.ports = [{ name: '80/tcp', bindings: [{ hostIP: '0.0.0.0', hostPort: '8080' }] }];
      const table = mount(SortableTable, {
        global: sortableTableGlobal(),
        props:  {
          headers:     (Containers as any).data.call({ t }).headers,
          rows:        rowsFor([web, container('db')], { getPortList: methods.getPortList }),
          keyField:    'id',
          rowsPerPage: 10,
        },
      }).vm;

      table.searchQuery = query;
      expect(table.filteredRows.map((row: any) => row.status.name)).toEqual(['web']);
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

    it.each([
      ['an empty bulk selection', []],
      ['no selection, as the row menu passes', undefined],
    ])('deletes the row\'s container given %s', async(_, selection) => {
      const containerDelete = jest.fn();
      const doomed = container('doomed');
      const row = rowFor(doomed, { containerDelete, confirmDelete: () => Promise.resolve(true) });

      await row.deleteContainer(selection);
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

    it('deletes what the dialog listed when the selection changes behind it', async() => {
      const containerDelete = jest.fn();
      const selection = [container('one'), container('two')];
      const doomed = [...selection];
      const row = rowFor(selection[0], {
        containerDelete,
        confirmDelete: () => {
          selection.splice(0, selection.length, container('other'));

          return Promise.resolve(true);
        },
      });

      await row.deleteContainer(selection);
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

describe('Containers cell clicks', () => {
  const { methods } = Containers as any;

  function clickEvent(modifiers: Record<string, boolean> = {}): any {
    return {
      shiftKey:        false,
      ctrlKey:         false,
      metaKey:         false,
      preventDefault:  jest.fn(),
      stopPropagation: jest.fn(),
      ...modifiers,
    };
  }

  function clickThatAddsToSelection(): any {
    return clickEvent({ ctrlKey: true, metaKey: true });
  }

  const clickedContainer = { id: 'some-container' };
  const publishedHostPort = 8080;

  function cellClickHelpers() {
    return {
      viewInfo: jest.fn(),
      openUrl:  jest.fn(),
    };
  }

  it('opens the info page when the container name is clicked', () => {
    const helpers = cellClickHelpers();

    methods.onContainerNameClick.call(helpers, clickEvent(), clickedContainer);

    expect(helpers.viewInfo).toHaveBeenCalledWith(clickedContainer);
  });

  it('leaves a shift-click on the container name to the table', () => {
    const helpers = cellClickHelpers();
    const event = clickEvent({ shiftKey: true });

    methods.onContainerNameClick.call(helpers, event, clickedContainer);

    expect(helpers.viewInfo).not.toHaveBeenCalled();
    expect(event.stopPropagation).not.toHaveBeenCalled();
  });

  it('leaves a click that adds to the selection to the table', () => {
    const helpers = cellClickHelpers();
    const event = clickThatAddsToSelection();

    methods.onContainerNameClick.call(helpers, event, clickedContainer);

    expect(helpers.viewInfo).not.toHaveBeenCalled();
    expect(event.stopPropagation).not.toHaveBeenCalled();
  });

  it('opens the published port when a port is clicked', () => {
    const helpers = cellClickHelpers();

    methods.onPortClick.call(helpers, clickEvent(), publishedHostPort);

    expect(helpers.openUrl).toHaveBeenCalledWith(publishedHostPort);
  });

  it('does not open the published port when the click selects rows', () => {
    const helpers = cellClickHelpers();

    methods.onPortClick.call(helpers, clickEvent({ shiftKey: true }), publishedHostPort);
    methods.onPortClick.call(helpers, clickThatAddsToSelection(), publishedHostPort);

    expect(helpers.openUrl).not.toHaveBeenCalled();
  });
});
