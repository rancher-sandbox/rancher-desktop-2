<script lang="ts" setup>

import { Component, computed, ComputedRef } from 'vue';
import { useStore } from 'vuex';

import PreferencesApplicationBehavior from '@pkg/components/Preferences/ApplicationBehavior.vue';
import PreferencesApplicationEnvironment from '@pkg/components/Preferences/ApplicationEnvironment.vue';
import PreferencesApplicationGeneral from '@pkg/components/Preferences/ApplicationGeneral.vue';
import RdTabbed from '@pkg/components/Tabbed/RdTabbed.vue';
import Tab from '@pkg/components/Tabbed/Tab.vue';

defineOptions({ name: 'preferences-body-application' });

type tabName = typeof store.state['transient-preferences']['navigation']['preferences']['application'];

const store = useStore();
const navigation = computed(() => store.state['transient-preferences'].navigation);
const activeTab = computed((): tabName => navigation.value?.preferences?.application || 'general');

const componentFromTab: ComputedRef<Component> = computed(() => {
  return ({
    general:     PreferencesApplicationGeneral,
    behavior:    PreferencesApplicationBehavior,
    environment: PreferencesApplicationEnvironment,
  } as const)[activeTab.value];
});

function tabSelected({ selectedName }: { selectedName: tabName }) {
  if (activeTab.value !== selectedName) {
    store.dispatch('transient-preferences/navigate', { 'preferences.application': selectedName })
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
    @changed="tabSelected"
  >
    <template #tabs>
      <tab
        label-key="preferences.tabs.environment"
        name="environment"
        :weight="1"
      />
      <!--
      <tab
        label-key="preferences.tabs.behavior"
        name="behavior"
        :weight="2"
      />
      -->
      <tab
        label-key="preferences.tabs.general"
        name="general"
        :weight="3"
      />
    </template>
    <div class="application-content">
      <component
        v-bind="$attrs"
        :is="componentFromTab"
      />
    </div>
  </rd-tabbed>
</template>

<style lang="scss" scoped>
  .application-content {
    padding: var(--preferences-content-padding);
  }
</style>
