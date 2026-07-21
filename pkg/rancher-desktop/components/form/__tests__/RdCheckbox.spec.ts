import { jest, describe, it, expect } from '@jest/globals';
import { mount } from '@vue/test-utils';
import { defineComponent } from 'vue';
import { createStore } from 'vuex';

import mockModules from '@pkg/utils/testUtils/mockModules';

mockModules({
  '@rancher/components': {
    Checkbox: defineComponent({
      name:  'Checkbox',
      props: {
        value:    { type: Boolean, default: false },
        disabled: { type: Boolean, default: false },
      },
      emits:    ['update:value'],
      template: `
        <label data-test="checkbox">
          <input
            type="checkbox"
            :checked="value"
            :disabled="disabled"
            @change="$emit('update:value', $event.target.checked)"
          >
          <span><slot name="label" /></span>
        </label>
      `,
    }),
  },
});

const { default: RdCheckbox } = await import('../RdCheckbox.vue');

type RdCheckboxProps = InstanceType<typeof RdCheckbox>['$props'];

const modify = jest.fn();
const writeNow = jest.fn();

function wrap(
  props: RdCheckboxProps,
  {
    preferences = {},
    locked = false,
    attrs = {},
  }: { preferences?: Record<string, any>; locked?: boolean; attrs?: Record<string, any> } = {},
) {
  const store = createStore({
    modules: {
      preferences: {
        namespaced: true,
        getters:    {
          preferences:        () => preferences,
          isPreferenceLocked: () => () => locked,
        },
        actions: {
          modify,
          writeNow,
        },
      },
    },
  });

  const wrapper = mount(RdCheckbox, {
    props,
    attrs,
    global: {
      plugins:    [store],
      stubs:      {
        TooltipIcon: true,
        t:           true,
      },
      directives: {
        'clean-tooltip': () => {},
        tooltip:         () => {},
      },
    },
  });

  return { wrapper };
}

describe('RdCheckbox.vue', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('should render label text', () => {
    const { wrapper } = wrap({
      preference: 'kubernetes.enabled',
      label:      'Some Unique Text',
    });

    expect(wrapper.get('[data-test="checkbox"]').text()).toContain('Some Unique Text');
  });

  it('should dispatch modify by default', async() => {
    const { wrapper } = wrap(
      {
        preference: 'kubernetes.enabled',
      },
      {
        preferences: { kubernetes: { enabled: false } },
      },
    );

    await wrapper.get('[data-test="checkbox"] input').setValue(true);

    expect(modify).toHaveBeenCalledTimes(1);
    expect(modify).toHaveBeenCalledWith(expect.anything(), {
      key:   'kubernetes.enabled',
      value: true,
    });
    expect(writeNow).not.toHaveBeenCalled();
  });

  it('should dispatch writeNow when immediate=true', async() => {
    const { wrapper } = wrap(
      {
        preference: 'kubernetes.enabled',
        immediate:  true,
      },
      {
        preferences: { kubernetes: { enabled: false } },
      },
    );

    await wrapper.get('[data-test="checkbox"] input').setValue(true);

    expect(writeNow).toHaveBeenCalledTimes(1);
    expect(writeNow).toHaveBeenCalledWith(expect.anything(), {
      key:   'kubernetes.enabled',
      value: true,
    });
    expect(modify).not.toHaveBeenCalled();
  });

  it('should be disabled when locked', async() => {
    const { wrapper } = wrap(
      {
        preference: 'kubernetes.enabled',
      },
      {
        locked: true,
      },
    );

    expect(wrapper.get('[data-test="checkbox"] input').attributes('disabled')).toBeDefined();

    await wrapper.get('[data-test="checkbox"] input').trigger('click');
    expect(modify).not.toHaveBeenCalled();
    expect(writeNow).not.toHaveBeenCalled();
  });
});
