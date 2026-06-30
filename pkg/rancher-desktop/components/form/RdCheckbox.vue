<script setup lang="ts">
import { Checkbox } from '@rancher/components';
import _ from 'lodash';
import { computed, PropType } from 'vue';
import { useStore } from 'vuex';

import TooltipIcon from '@pkg/components/form/TooltipIcon.vue';
import { RecursiveLeafKeysOfType } from '@pkg/utils/typeUtils';

import type { IoRancherdesktopAppV1alpha1AppSpec as AppSpec } from '@rdd-client';

defineOptions({
  name:         'rd-checkbox',
  inheritAttrs: false,
});

const { preference: preferenceName, isExperimental, tooltip, labelKey, label, tooltipKey } = defineProps({
  preference:      {
    type:     String as PropType<RecursiveLeafKeysOfType<AppSpec, boolean | undefined>>,
    required: true,
  },
  isExperimental: {
    type:    Boolean,
    default: false,
  },
  tooltip: {
    type:    String,
    default: undefined,
  },
  labelKey: {
    type:    String,
    default: undefined,
  },
  label: {
    type:    String,
    default: undefined,
  },
  tooltipKey: {
    type:    String,
    default: undefined,
  },
});

const store = useStore();
const preferences = computed(() => store.getters['preferences/preferences']);
const value = computed(() => !!_.get(preferences.value, preferenceName, false));
const isLocked = computed(() => store.getters['preferences/isPreferenceLocked'](preferenceName));

function onUpdate(value: boolean) {
  store.dispatch('preferences/modify', { key: preferenceName, value });
}

</script>

<template>
  <div class="rd-checkbox-container">
    <checkbox
      v-bind="$attrs"
      class="checkbox"
      :disabled="$attrs.disabled || isLocked"
      :value="value"
      @update:value="onUpdate"
    >
      <template #label>
        <slot name="label">
          <t
            v-if="labelKey"
            :k="labelKey"
            :raw="true"
          />
          <template v-else-if="label">
            {{ label }}
          </template>
          <i
            v-if="tooltipKey"
            v-clean-tooltip="t(tooltipKey)"
            class="checkbox-info icon icon-info icon-lg"
          />
          <i
            v-else-if="tooltip"
            v-clean-tooltip="tooltip"
            class="checkbox-info icon icon-info icon-lg"
          />
        </slot>
        <slot name="after">
          <tooltip-icon
            v-if="isExperimental"
            class="tooltip-icon"
          />
          <i
            v-if="isLocked"
            v-tooltip="{
              content: tooltip || t('preferences.locked.tooltip', undefined, true),
              placement: 'right',
            }"
            class="icon icon-lock"
          />
        </slot>
      </template>
    </checkbox>
    <div class="checkbox-below">
      <slot name="below" />
    </div>
  </div>
</template>

<style lang="scss" scoped>
.checkbox :deep(.checkbox-outer-container-description) {
  font-size: 11px;
}
.tooltip-icon {
  margin-left: 0.25rem;
}
.checkbox-below {
  margin-left: 19px;
  font-size: 11px;
  &:empty {
    display: none;
  }
}

</style>
