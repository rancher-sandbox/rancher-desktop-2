import { mount } from '@vue/test-utils';
import { nextTick, ref } from 'vue';
import { createStore } from 'vuex';

import cleanHtmlDirective from '@pkg/plugins/clean-html-directive';
import { binDirInjectionKey } from '@pkg/types/injectionKeys';
import mockModules from '@pkg/utils/testUtils/mockModules';
import { IoRancherdesktopAppV1alpha1AppSpecApplicationAddPathEnum as AddPath } from '@rdd-client';

mockModules({
  '@pkg/entry/store': undefined,
});

const { default: PathManagementSelector } = await import('@pkg/components/PathManagementSelector.vue');
const { default: RadioGroup } = await import('@pkg/components/form/RadioGroup.vue');
const { default: RadioButton } = await import('@pkg/components/form/RadioButton.vue');

const options = Object.values(AddPath);

function setup() {
  const store = createStore({
    state: () => ({
      preferences: {
        application: {
          addPath: options[0],
        },
      },
      locked: false,
    }),
    getters: {
      'i18n/t':                         () => (key: string, args: Record<string, any> = {}) => `${ key }: ${ args?.binDir ?? '' }`,
      'preferences/preferences':        state => state.preferences,
      'preferences/isPreferenceLocked': state => (key: string) =>
        state.locked && key === 'application.addPath',
    },
    mutations: {
      setAddPath(state, value: AddPath) {
        state.preferences.application.addPath = value;
      },
      setLocked(state, value: boolean) {
        state.locked = value;
      },
    },
    actions: {
      'preferences/modify'({ commit }, { key, value }) {
        if (key === 'application.addPath') {
          commit('setAddPath', value);
        }
      },
    },
  });

  const wrapper = mount(PathManagementSelector, {
    global: {
      plugins:    [store, cleanHtmlDirective],
      components: {
        t: (key: string) => key,
      },
      directives: {
        'clean-tooltip': {},
      },
      provide: {
        [binDirInjectionKey]: ref('BIN_DIR'),
      },
    },
  });

  return { wrapper, store };
}

describe('PathManagementSelector', () => {
  it('renders all three options', async() => {
    const { wrapper } = setup();
    // Allow time for binDir to be resolved.
    await nextTick();
    const buttons = wrapper.findAllComponents(RadioButton);

    expect(buttons.map(button => button.props('val'))).toEqual(options);

    for (const button of buttons) {
      expect(button.find('input[type="radio"]').exists()).toBe(true);
      expect(button.text()).toContain(`pathManagement.options.${ button.props('val') }`);
      expect(button.text()).toContain('BIN_DIR');
    }
  });

  it('updates the radio-group value when each option is clicked', async() => {
    const { wrapper } = setup();
    const group = wrapper.getComponent(RadioGroup);
    const buttons = wrapper.findAllComponents(RadioButton);

    expect(group.props('value')).toBe(options[0]);

    // Select the initially unselected options before returning to the first.
    for (const index of [1, 2, 0]) {
      const input = buttons[index].get('[role="radio"]');

      (input.element as HTMLInputElement).click();
      await nextTick();

      expect(group.props('value')).toBe(options[index]);
    }
  });

  it('disables the selector when the preference becomes locked', async() => {
    const { wrapper, store } = setup();
    const group = wrapper.getComponent(RadioGroup);

    expect(group.props('disabled')).toBe(false);

    store.commit('setLocked', true);
    await nextTick();

    expect(group.props('disabled')).toBe(true);

    for (const button of wrapper.findAllComponents(RadioButton)) {
      expect(button.props('disabled')).toBe(true);
      const input = button.get('input[type="radio"]').element as HTMLInputElement;
      expect(input.disabled).toBe(true);
    }

    store.commit('setLocked', false);
    await nextTick();

    expect(group.props('disabled')).toBe(false);

    for (const button of wrapper.findAllComponents(RadioButton)) {
      expect(button.props('disabled')).toBe(false);
      const input = button.get('input[type="radio"]').element as HTMLInputElement;
      expect(input.disabled).toBe(false);
    }
  });
});
