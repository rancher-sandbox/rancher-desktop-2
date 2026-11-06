// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { t } from '@pkg/utils/testUtils/translations';

const componentStub = { template: '<div />' };
const showMessageBox = jest.fn<(channel: string, options: any) => Promise<{ response: number }>>();

mockModules({
  '@pkg/components/ImagesOutputWindow.vue': componentStub,
  '@pkg/entry/store':                       {
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

const { default: Images } = await import('@pkg/components/Images.vue');

describe('Images deleteImages', () => {
  const deleteButton = 0;
  const cancelButton = 1;

  beforeEach(() => {
    showMessageBox.mockReset();
  });

  /** A `this` for deleteImages whose selection is the given (live) array. */
  function context(selected: { id: string, reference: string }[]) {
    const ctx = {
      t,
      selected,
      imageDelete: jest.fn<(arg: { image: unknown }) => Promise<void>>().mockResolvedValue(undefined),
      get imagesToDelete() {
        return (Images as any).computed.imagesToDelete.call(ctx);
      },
      isDeletable: () => true,
    };

    return ctx;
  }

  it('deletes what the dialog listed when the selection changes behind it', async() => {
    const one = { id: 'sha256:1', reference: 'one:latest' };
    const two = { id: 'sha256:2', reference: 'two:latest' };
    const selected = [one, two];
    const ctx = context(selected);

    showMessageBox.mockImplementation(() => {
      selected.splice(0, selected.length, { id: 'sha256:3', reference: 'other:latest' });

      return Promise.resolve({ response: deleteButton });
    });
    await (Images as any).methods.deleteImages.call(ctx);

    expect(showMessageBox).toHaveBeenCalledWith('show-message-box', expect.objectContaining({
      message: 'Delete 2 images?',
      detail:  'one:latest\ntwo:latest',
    }));
    expect(ctx.imageDelete.mock.calls).toEqual([[{ image: one }], [{ image: two }]]);
  });

  it('deletes nothing when the user cancels', async() => {
    const ctx = context([{ id: 'sha256:1', reference: 'one:latest' }]);

    showMessageBox.mockResolvedValue({ response: cancelButton });
    await (Images as any).methods.deleteImages.call(ctx);

    expect(ctx.imageDelete).not.toHaveBeenCalled();
  });
});
