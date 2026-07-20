import { jest } from '@jest/globals';
import { mount } from '@vue/test-utils';
import FloatingVue from 'floating-vue';
import _ from 'lodash';
import { createStore } from 'vuex';

import type { UpdateState } from '@pkg/main/update';
import mockModules from '@pkg/utils/testUtils/mockModules';
import { FieldType, RecursiveLeafKeys } from '@pkg/utils/typeUtils.js';
import { IoRancherdesktopAppV1alpha1AppSpec as AppSpec } from '@rdd-client';

mockModules({
  '@pkg/utils/ipcRenderer': {
    ipcRenderer: {
      on:   jest.fn(),
      send: jest.fn(),
    },
  },
  electron: undefined,
});

const { default: UpdateStatus } = await import('../UpdateStatus.vue');

type PropType = InstanceType<typeof UpdateStatus>['$props'];
type PrefInputs = { [K in RecursiveLeafKeys<AppSpec>]?: FieldType<AppSpec, K> };

function wrap(props: PropType, prefs: PrefInputs) {
  const store = createStore({
    getters: {
      'preferences/preferences': () => {
        const result = {};
        for (const [key, value] of Object.entries(prefs)) {
          _.set(result, key, value);
        }
        return result;
      },
    },
  });
  return mount(UpdateStatus, {
    props,
    global: {
      mocks:   { t: jest.fn() },
      stubs:   {
        T:          { template: '<span> {{ k }} </span>' },
        RdCheckbox: { template: '<input type="checkbox">' },
        Version:    { template: '<span />' },
      },
      plugins: [store, FloatingVue],
    },
  });
}

describe('UpdateStatus.vue', () => {
  describe('update visibility', () => {
    it('shows updates when available', () => {
      const wrapper = wrap({
        preference:  'application.updates.enabled',
        updateState: { available: true, downloaded: true } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.findComponent({ ref: 'updateInfo' }).exists()).toBeTruthy();
    });

    it('hides updates when disabled', () => {
      const wrapper = wrap({
        preference:  'application.updates.enabled',
        updateState: { available: true, downloaded: true } as UpdateState,
      }, { 'application.updates.enabled': false });

      expect(wrapper.findComponent({ ref: 'updateInfo' }).exists()).toBeFalsy();
    });

    it('hides when no updates are available', () => {
      const wrapper = wrap({
        preference:  'application.updates.enabled',
        updateState: { available: false, downloaded: false } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.findComponent({ ref: 'updateInfo' }).exists()).toBeFalsy();
    });
  });

  describe('update status', () => {
    it('displays error correctly', () => {
      const wrapper = wrap({
        preference:  'application.updates.enabled',
        updateState: {
          available: true, error: new Error('hello'), downloaded: true,
        } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.get({ ref: 'updateStatus' }).text())
        .toEqual('There was an error checking for updates.');
      expect(wrapper.element.querySelector('.update-notification'))
        .toBeFalsy();
    });

    it('hides when there is nothing to display', () => {
      const wrapper = wrap({
        preference:     'application.updates.enabled',
        updateState: { available: true } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.get({ ref: 'updateStatus' }).text())
        .toEqual('');
    });

    it('shows when an update is available', () => {
      const wrapper = wrap({
        preference:     'application.updates.enabled',
        updateState: {
          available: true, downloaded: true, info: { version: 'v1.2.3' },
        } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.get({ ref: 'updateStatus' }).text().replace(/\s+/g, ' '))
        .toEqual('An update to version v1.2.3 is available. Restart the application to apply the update.');

      expect(wrapper.get({ ref: 'applyButton' }).attributes()).not.toHaveProperty('disabled');
    });

    it('does not allow applying again', async() => {
      const wrapper = wrap({
        preference:     'application.updates.enabled',
        updateState: {
          available: true, downloaded: true, info: { version: 'v1.2.3' },
        } as UpdateState,
      }, { 'application.updates.enabled': true });

      await wrapper.get({ ref: 'applyButton' }).trigger('click');
      expect(wrapper.get({ ref: 'applyButton' }).attributes()).toHaveProperty('disabled');
    });

    it('shows download progress', () => {
      const wrapper = wrap({
        preference:     'application.updates.enabled',
        updateState: {
          configured: true,
          available:  true,
          downloaded: false,
          info:       {
            version:                    'v1.2.3',
            files:                      [],
            path:                       '',
            sha512:                     '',
            releaseDate:                '',
            nextUpdateTime:             12345,
            unsupportedUpdateAvailable: false,
          },
          progress: {
            percent:        12.34,
            bytesPerSecond: 1234567,
            total:          0,
            delta:          0,
            transferred:    0,
          },
        } as UpdateState,
        locale: 'en',
      }, { 'application.updates.enabled': true });

      expect(wrapper.get({ ref: 'updateStatus' }).text())
        .toMatch(/^An update to version v1\.2\.3 is available; downloading... \(12%, 1\.2MB\/s(?:ec\.?)?\)$/);
      expect(wrapper.find({ ref: 'applyButton' }).exists()).toBeFalsy();
    });
  });

  describe('release notes', () => {
    it('should not be displayed if there are none', () => {
      const wrapper = wrap({
        preference:  'application.updates.enabled',
        updateState: { info: { version: 'v1.2.3' } } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.find({ ref: 'releaseNotes' }).exists()).toBeFalsy();
    });

    it('should render plain text', () => {
      const wrapper = wrap({
        preference:     'application.updates.enabled',
        updateState: {
          available: true,
          info:      { version: 'v1.2.3', releaseNotes: 'hello' },
        } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.get({ ref: 'releaseNotes' }).text())
        .toEqual('hello');
    });

    it('should render markdown', () => {
      const wrapper = wrap({
        preference:     'application.updates.enabled',
        updateState: {
          available: true,
          info:      { version: 'v1.2.3', releaseNotes: '**hello**' },
        } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.get({ ref: 'releaseNotes' }).html())
        .toContain('<strong>hello</strong>');
    });

    it('should not support scripting', () => {
      const wrapper = wrap({
        preference:     'application.updates.enabled',
        updateState: {
          available: true,
          info:      {
            version:      'v1.2.3',
            releaseNotes: 'hello<script>alert(1)</script><img onload="alert(2)">',
          },
        } as UpdateState,
      }, { 'application.updates.enabled': true });

      expect(wrapper.get({ ref: 'releaseNotes' }).html())
        .not.toContain('alert');
    });
  });
});
