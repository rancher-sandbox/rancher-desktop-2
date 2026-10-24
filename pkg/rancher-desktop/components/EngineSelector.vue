<script lang="ts" setup>
import { computed } from 'vue';
import { useStore } from 'vuex';

import RadioGroup from '@pkg/components/form/RadioGroup.vue';
import { IoRancherdesktopAppV1alpha1AppSpecContainerEngineNameEnum as ContainerEngineName } from '@rdd-client';

defineOptions({ name: 'engine-selector' });

const store = useStore();
const t = computed(() => store.getters['i18n/t']);

const {
  disabled = false,
  row = false,
} = defineProps({
  disabled: Boolean,
  row:      Boolean,
});

const containerEngine = computed(() => store.getters['preferences/preferences']?.containerEngine?.name);
const isLocked = computed(() => store.getters['preferences/isPreferenceLocked']('containerEngine.name'));
const options = computed(() => Object.values(ContainerEngineName)
  .map(value => ({
    label:       t.value(`containerEngine.options.${ value }.label`),
    value,
    description: t.value(`containerEngine.options.${ value }.description`),
  })));

async function onEngineChange(value: ContainerEngineName) {
  try {
    await store.dispatch('preferences/modify', {
      key: 'containerEngine.name',
      value,
    });
  } catch (ex) {
    console.error(ex);
  }
}

</script>

<template>
  <div
    class="engine-selector"
    :data-value="containerEngine"
  >
    <radio-group
      name="containerEngine"
      class="container-engine"
      :class="{ 'locked-radio': isLocked }"
      :value="containerEngine"
      :options="options"
      :row="row"
      :disabled="disabled || isLocked"
      @update:value="onEngineChange"
    >
      <template #label>
        <slot name="label" />
      </template>
    </radio-group>
  </div>
</template>

<style lang="scss" scoped>
.container-engine :deep(label) {
  color: var(--input-label);
}
</style>
