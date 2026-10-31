// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { jest } from '@jest/globals';
import { mount } from '@vue/test-utils';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { sortableTableGlobal } from '@pkg/utils/testUtils/sortableTable';
import { t } from '@pkg/utils/testUtils/translations';
import { IoRancherdesktopContainersV1alpha1Volume as Volume } from '@rdd-client';

const componentStub = { template: '<div />' };
const showMessageBox = jest.fn<(channel: string, options: any) => Promise<{ response: number }>>();

mockModules({
  '@pkg/components/Banner.vue': componentStub,
  '@pkg/entry/store':           {
    mapTypedActions:   jest.fn(() => ({})),
    mapTypedGetters:   jest.fn(() => ({})),
    mapTypedMutations: jest.fn(() => ({})),
    mapTypedState:     jest.fn(() => ({})),
  },
  '@pkg/utils/ipcRenderer': {
    ipcRenderer: {
      on:             jest.fn(),
      send:           jest.fn(),
      invoke:         showMessageBox,
      removeListener: jest.fn(),
    },
  },
});

const { default: Volumes } = await import('@pkg/pages/Volumes.vue');

describe('Volumes methods', () => {
  beforeEach(() => {
    showMessageBox.mockReset();
  });

  function volume(name: string, driver = 'local', mountpoint = `/var/lib/docker/volumes/${ name }/_data`): Volume {
    return {
      metadata: { name: `${ name }-id`, namespace: 'default', uid: `${ name }-uid` },
      status:   {
        name,
        namespace:  'default',
        driver,
        mountpoint,
        scope:      'local',
      },
    };
  }

  it('names and counts the volumes in the confirmation, and defaults to Cancel', async() => {
    const cancelButton = 1;

    showMessageBox.mockResolvedValue({ response: cancelButton });
    await expect((Volumes as any).methods.confirmDelete.call({ t }, [volume('one'), volume('two')]))
      .resolves.toBe(false);
    expect(showMessageBox).toHaveBeenCalledWith('show-message-box', expect.objectContaining({
      message:             'Delete 2 volumes?',
      detail:              'one\ntwo',
      buttons:             ['&Delete', 'Cancel'],
      cancelId:            cancelButton,
      defaultId:           cancelButton,
      normalizeAccessKeys: true,
    }));
  });

  it('reports a confirmed deletion', async() => {
    const deleteButton = 0;

    showMessageBox.mockResolvedValue({ response: deleteButton });
    await expect((Volumes as any).methods.confirmDelete.call({ t }, [volume('one')]))
      .resolves.toBe(true);
  });

  describe('table', () => {
    /** Mount the page with the given volumes, and return its table. */
    function mountTable(volumes: Volume[]) {
      const wrapper = mount(Volumes, {
        global: sortableTableGlobal({
          error:              null,
          supportsNamespaces: false,
          volumes,
          watchResources:     () => Promise.resolve(),
          setHeader:          jest.fn(),
        }),
      });

      return wrapper.findComponent({ name: 'SortableTable' });
    }

    it('keys each row by its volume UID', () => {
      const rows = mountTable([volume('one'), volume('two')]).findAll('tr.main-row');

      expect(rows.map(row => row.attributes('data-node-id'))).toEqual(['one-uid', 'two-uid']);
    });

    it.each([
      ['name', 'two'],
      ['driver', 'zfs'],
      ['mountpoint', 'data7'],
    ])('finds a volume by its %s', (_, query) => {
      // Each query matches only the one field that the test names.
      const table = mountTable([volume('one'), volume('two', 'zfs', '/mnt/data7')]).vm;

      table.searchQuery = query;
      expect(table.filteredRows.map((row: any) => row.status.name)).toEqual(['two']);
    });

    it('sorts by driver', () => {
      const table = mountTable([volume('one', 'zfs'), volume('two', 'local')]).vm;

      table.sortBy = 'Driver';
      expect(table.arrangedRows.map((row: any) => row.status.name)).toEqual(['two', 'one']);
    });
  });

  describe('deletion', () => {
    /** Build the row SortableTable would render for a single volume. */
    function rowFor(item: Volume, context: any) {
      const self = {
        volumes: [item],
        t,
        ...context,
      };

      return (Volumes as any).computed.rows.call(self)[0];
    }

    it('confirms the whole bulk selection, and deletes none of it when cancelled', async() => {
      const volumeDelete = jest.fn();
      const confirmDelete = jest.fn<(targets: Volume[]) => Promise<boolean>>().mockResolvedValue(false);
      const doomed = [volume('one'), volume('two')];
      const row = rowFor(doomed[0], { volumeDelete, confirmDelete });

      await row.deleteVolume(doomed);
      expect(confirmDelete).toHaveBeenCalledWith(doomed);
      expect(volumeDelete).not.toHaveBeenCalled();
    });

    it('deletes the whole bulk selection once the user confirms', async() => {
      const volumeDelete = jest.fn();
      const doomed = [volume('one'), volume('two')];
      const row = rowFor(doomed[0], { volumeDelete, confirmDelete: () => Promise.resolve(true) });

      await row.deleteVolume(doomed);
      expect(volumeDelete.mock.calls).toEqual(doomed.map(target => [{ volume: target }]));
    });

    it('deletes what the dialog listed when the selection changes behind it', async() => {
      const volumeDelete = jest.fn();
      const selection = [volume('one'), volume('two')];
      const doomed = [...selection];
      const row = rowFor(selection[0], {
        volumeDelete,
        confirmDelete: () => {
          selection.splice(0, selection.length, volume('other'));

          return Promise.resolve(true);
        },
      });

      await row.deleteVolume(selection);
      expect(volumeDelete.mock.calls).toEqual(doomed.map(target => [{ volume: target }]));
    });

    it.each([
      ['an empty bulk selection', []],
      ['no selection, as the row menu passes', undefined],
    ])('deletes the row\'s volume given %s', async(_, selection) => {
      const volumeDelete = jest.fn();
      const single = volume('single');
      const row = rowFor(single, { volumeDelete, confirmDelete: () => Promise.resolve(true) });

      await row.deleteVolume(selection);
      expect(volumeDelete).toHaveBeenCalledWith({ volume: single });
    });
  });
});
