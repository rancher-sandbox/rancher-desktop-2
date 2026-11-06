<script setup lang="ts">
import { computed, inject, ref } from 'vue';
import { useStore } from 'vuex';

import RadioButton from '@pkg/components/form/RadioButton.vue';
import RadioGroup from '@pkg/components/form/RadioGroup.vue';
import { binDirInjectionKey } from '@pkg/types/injectionKeys';
import { escapeHtml } from '@pkg/utils/string';
import { IoRancherdesktopAppV1alpha1AppSpecApplicationAddPathEnum as AddPath } from '@rdd-client';

defineOptions({ name: 'path-management-selector' });

const {
  row = false,
  showLabel = true,
  disabled = false,
} = defineProps<{
  row?:       boolean,
  showLabel?: boolean,
  disabled?:  boolean,
}>();

const store = useStore();
const t = computed(() => store.getters['i18n/t']);
const preferences = computed(() => store.getters['preferences/preferences']);
const isPreferenceLocked = computed(() => store.getters['preferences/isPreferenceLocked']);
const isLocked = computed(() => !!isPreferenceLocked.value?.('application.addPath'));
const currentValue = computed(() => preferences.value.application?.addPath ?? AddPath.Manual);

const binDir = inject(binDirInjectionKey, ref('~/.rd2/bin'));

const options = computed(() =>
  Object.values(AddPath).map(key => ({
    label:       t.value(`pathManagement.options.${ key }.label`),
    value:       key,
    description: t.value(`pathManagement.options.${ key }.description`, { binDir: escapeHtml(binDir.value) }),
  } as const)));

const groupName = 'pathManagement';

const label = computed(() => showLabel ? t.value('pathManagement.label') : '');
const tooltip = computed(() => showLabel
  ? { content: t.value('pathManagement.tooltip', { binDir: escapeHtml(binDir.value) }), html: true }
  : '');

function updateVal(value: AddPath) {
  store.dispatch('preferences/modify', { key: 'application.addPath', value });
}
</script>

<template>
  <radio-group
    :name="groupName"
    :label="label"
    :tooltip="tooltip"
    :value="currentValue"
    :options="options"
    :row="row"
    :disabled="isLocked || disabled"
    data-testid="path-management-selector"
    :data-value="currentValue"
    :class="{ 'locked-radio': isLocked }"
    class="path-management"
    @update:value="updateVal"
  >
    <template
      v-if="showLabel && $slots.label"
      #label
    >
      <slot name="label" />
    </template>
    <template
      v-for="(option, index) in options"
      #[index]="{ isDisabled }"
      :key="groupName + '-' + option.value"
    >
      <radio-button
        :name="groupName"
        :value="currentValue"
        :label="option.label"
        :val="option.value"
        :disabled="isDisabled"
        :data-testid="`path-management-${option.value}`"
        @update:value="updateVal(option.value)"
      >
        <template #description>
          <span
            v-clean-html="option.description"
            data-testid="description"
          />
        </template>
      </radio-button>
    </template>
  </radio-group>
</template>

<style lang="scss" scoped>
.path-management :deep(code) {
  user-select: text;
  cursor: text;
  padding: 2px;
}

.path-management :deep(.radio-label) {
  color: var(--input-label);
}
</style>
