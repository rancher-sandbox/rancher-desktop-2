import { jest } from '@jest/globals';
import { shallowMount } from '@vue/test-utils';
import { createStore } from 'vuex';

import mockModules from '@pkg/utils/testUtils/mockModules';
import { t } from '@pkg/utils/testUtils/translations';

const ipcRenderer = {
  on:   jest.fn(),
  send: jest.fn(),
};

mockModules({
  '@pkg/utils/ipcRenderer': { ipcRenderer },
});

const { default: FatalError } = await import('@pkg/pages/FatalError.vue');

/**
 * Build a store with just enough of `i18n` for `FatalError.vue` to mount.
 */
function makeStore() {
  return createStore({
    getters: {
      'i18n/t': () => t,
    },
  });
}

/**
 * `ipcRenderer.on('dialog/populate', ...)` is registered in <script setup>,
 * which only runs once the component is mounted; grab the listener that was
 * registered for the wrapper under test so it can be invoked directly.
 */
function getPopulateListener() {
  const call = ipcRenderer.on.mock.calls.findLast(([channel]) => channel === 'dialog/populate');

  if (!call) {
    throw new Error("ipcRenderer.on('dialog/populate', ...) was not registered");
  }

  return call[1] as (event: unknown, data: { title: string, message: string, details?: string }) => void;
}

describe('FatalError.vue', () => {
  afterEach(() => {
    jest.restoreAllMocks();
    ipcRenderer.on.mockClear();
    ipcRenderer.send.mockClear();
  });

  it('shows placeholder text until the main process populates the dialog', () => {
    const wrapper = shallowMount(FatalError, {
      global: {
        plugins:    [makeStore()],
        directives: {
          focus:         {},
          'clean-html':  {},
        },
      },
    });

    expect(wrapper.find('#message-title').text()).toBe(t('fatalError.titleUnknown'));
    expect(wrapper.find('#message-body').text()).toBe(t('fatalError.messageUnknown'));
    expect(wrapper.find('details').exists()).toBe(false);
  });

  it('updates the title, message, and details when dialog/populate is received', async() => {
    const wrapper = shallowMount(FatalError, {
      global: {
        plugins:    [makeStore()],
        directives: {
          focus:         {},
          'clean-html':  {},
        },
      },
    });

    const populate = getPopulateListener();

    populate({}, { title: 'SomeReason', message: 'Something went wrong.', details: 'stack trace details' });
    await wrapper.vm.$nextTick();

    expect(wrapper.find('#message-title').text()).toBe('SomeReason');
    expect(wrapper.find('#message-body').text()).toBe('Something went wrong.');
    expect(wrapper.find('details').exists()).toBe(true);
    expect(wrapper.find('pre').text()).toBe('stack trace details');
  });

  it('omits the details section when no failure details are provided', async() => {
    const wrapper = shallowMount(FatalError, {
      global: {
        plugins:    [makeStore()],
        directives: {
          focus:         {},
          'clean-html':  {},
        },
      },
    });

    const populate = getPopulateListener();

    populate({}, { title: 'SomeReason', message: 'Something went wrong.' });
    await wrapper.vm.$nextTick();

    expect(wrapper.find('details').exists()).toBe(false);
  });

  it('sends error/show-logs when the "Show Logs" button is clicked', async() => {
    const wrapper = shallowMount(FatalError, {
      global: {
        plugins:    [makeStore()],
        directives: {
          focus:         {},
          'clean-html':  {},
        },
      },
    });

    await wrapper.find('button.role-secondary').trigger('click');

    expect(ipcRenderer.send).toHaveBeenCalledWith('error/show-logs');
  });

  it('closes the window when the "Close" button is clicked', async() => {
    jest.spyOn(window, 'close').mockImplementation(() => {});

    const wrapper = shallowMount(FatalError, {
      global: {
        plugins:    [makeStore()],
        directives: {
          focus:         {},
          'clean-html':  {},
        },
      },
    });

    await wrapper.find('button.role-primary').trigger('click');

    expect(window.close).toHaveBeenCalled();
  });
});
