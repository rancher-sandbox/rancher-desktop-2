<script lang="ts" setup>

import { Component, computed } from 'vue';
import { useStore } from 'vuex';

import PreferencesVirtualMachineEmulation from '@pkg/components/Preferences/VirtualMachineEmulation.vue';
import PreferencesVirtualMachineHardware from '@pkg/components/Preferences/VirtualMachineHardware.vue';
import PreferencesVirtualMachineVolumes from '@pkg/components/Preferences/VirtualMachineVolumes.vue';
import RdTabbed from '@pkg/components/Tabbed/RdTabbed.vue';
import Tab from '@pkg/components/Tabbed/Tab.vue';

import type { ComputedRef } from 'vue';

defineOptions({ name: 'preferences-body-virtual-machine' });

type tabName = typeof store.state['transient-preferences']['navigation']['preferences']['virtual-machine'];

const store = useStore();
const preferences = computed(() => store.getters['preferences/preferences']);
const navigation = computed(() => store.state['transient-preferences'].navigation);
const activeTab = computed((): tabName => navigation.value?.preferences?.['virtual-machine'] || 'hardware');

const componentFromTab: ComputedRef<Component> = computed(() => {
  return ({
    hardware:  PreferencesVirtualMachineHardware,
    volumes:   PreferencesVirtualMachineVolumes,
    emulation: PreferencesVirtualMachineEmulation,
  } as const)[activeTab.value];
});

function tabSelected({ selectedName }: { selectedName: tabName }) {
  if (activeTab.value !== selectedName) {
    store.dispatch('transient-preferences/navigate', { 'preferences.virtual-machine': selectedName })
      // TODO: Actual error handling
      // https://github.com/rancher-sandbox/rancher-desktop-2/issues/574
      .catch(console.error);
  }
}

</script>

<template>
  <rd-tabbed
    v-bind="$attrs"
    class="action-tabs"
    :no-content="true"
    :default-tab="activeTab"
    :active-tab="activeTab"
    @changed="tabSelected"
  >
    <template #tabs>
      <tab
        label="Hardware"
        name="hardware"
        :weight="4"
      />
      <!--
      <tab
        label="Volumes"
        name="volumes"
        :weight="3"
      />
      <tab
        v-if="isPlatformDarwin"
        label="Emulation"
        name="emulation"
        :weight="1"
      />
      -->
    </template>
    <div class="virtual-machine-content">
      <component
        v-bind="$attrs"
        :is="componentFromTab"
        :preferences="preferences"
      />
    </div>
  </rd-tabbed>
</template>

<style lang="scss" scoped>
  .virtual-machine-content {
    padding: var(--preferences-content-padding);
  }
</style>
