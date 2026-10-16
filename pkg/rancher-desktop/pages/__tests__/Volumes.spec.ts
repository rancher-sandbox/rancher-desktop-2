import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { t } from '@pkg/utils/testUtils/translations';
import { IoRancherdesktopContainersV1alpha1Volume as Volume } from '@rdd-client';

const componentStub = { template: '<div />' };
const showMessageBox = jest.fn<(channel: string, options: any) => Promise<{ response: number }>>();

mockModules({
  '@pkg/components/Banner.vue':    componentStub,
  '@pkg/components/SortableTable': componentStub,
  '@pkg/entry/store':              {
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

  function volume(name: string): Volume {
    return {
      metadata: { name: `${ name }-id`, namespace: 'default' },
      status:   {
        name,
        namespace:  'default',
        driver:     'local',
        mountpoint: `/var/lib/docker/volumes/${ name }/_data`,
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
