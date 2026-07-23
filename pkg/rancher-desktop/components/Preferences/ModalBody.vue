<script lang="ts" setup>

import { computed, PropType, ComputedRef, Component } from 'vue';

import PreferencesBodyApplication from '@pkg/components/Preferences/BodyApplication.vue';
import PreferencesBodyContainerEngine from '@pkg/components/Preferences/BodyContainerEngine.vue';
import PreferencesBodyKubernetes from '@pkg/components/Preferences/BodyKubernetes.vue';
import PreferencesBodyVirtualMachine from '@pkg/components/Preferences/BodyVirtualMachine.vue';
import PreferencesBodyWsl from '@pkg/components/Preferences/BodyWsl.vue';
import PreferencesHelp from '@pkg/components/Preferences/Help.vue';
import type { preferencesNavItemName } from '@pkg/window/preferenceConstants';

defineOptions({ name: 'preferences-body' });

const { currentNavItem } = defineProps({
  currentNavItem: {
    type:     String as PropType<preferencesNavItemName>,
    required: true,
  },
});

const componentFromNavItem: ComputedRef<Component> = computed(() => {
  return ({
    Application:        PreferencesBodyApplication,
    'Container Engine': PreferencesBodyContainerEngine,
    Kubernetes:         PreferencesBodyKubernetes,
    'Virtual Machine':  PreferencesBodyVirtualMachine,
    WSL:                PreferencesBodyWsl,
  } as const)[currentNavItem];
});
</script>

<template>
  <div
    class="preferences-body"
    data-testid="preferences-body"
    :data-test-component="currentNavItem"
  >
    <slot>
      <component
        v-bind="$attrs"
        :is="componentFromNavItem"
      />
    </slot>
    <preferences-help class="help" />
  </div>
</template>

<style lang="scss" scoped>
  .preferences-body {
    position: relative;
    display: flex;
    flex-direction: column;

    .help {
      position: absolute;
      bottom: 0.75rem;
      right: 0.75rem;
    }
  }
</style>
