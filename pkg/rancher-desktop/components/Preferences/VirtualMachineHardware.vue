<script lang="ts" setup>
import { computed, onBeforeMount, onBeforeUnmount } from 'vue';
import { useStore } from 'vuex';

import SystemPreferences from '@pkg/components/SystemPreferences.vue';
import { quantityToScalar } from '@rdd-client';

defineOptions({ name: 'preferences-virtual-machine-hardware' });

const gigaScale = BigInt(2) ** BigInt(30);
const store = useStore();
const preferences = computed(() => store.getters['preferences/preferences']);
const isPreferenceLocked = computed(() => store.getters['preferences/isPreferenceLocked']);
const memoryInGB = computed(() => {
  // quantityToScalar will return BigInts in our case; convert it to gigabytes,
  // at which point it should be reasonable for us.
  const memoryInBytes = BigInt(quantityToScalar(preferences.value?.virtualMachine?.memory ?? '2Gi'));
  return Number(memoryInBytes / gigaScale);
});
const hostInfo = computed(() => store.state.rdd.hostInfos?.[0]);
const availMemoryInGB = computed(() => {
  const memoryInBytes = BigInt(hostInfo.value?.status?.memory || BigInt(2) * gigaScale);
  return Number(memoryInBytes / gigaScale);
});
const availNumCPUs = computed(() => hostInfo.value?.status?.cpus || 1);

function setCPUs(value: number) {
  store.dispatch('preferences/modify', { key: 'virtualMachine.cpus', value });
}

function setMemory(value: number) {
  // The value is in GB; always add the `Gi` suffix when storing it.
  store.dispatch('preferences/modify', { key: 'virtualMachine.memory', value: value + 'Gi' });
}

onBeforeMount(() => {
  store.dispatch('rdd/watchResources', ['hostInfos']);
});

onBeforeUnmount(() => {
  store.dispatch('rdd/unwatchResources', ['hostInfos']);
});

</script>

<template>
  <div class="virtual-machine-hardware">
    <system-preferences
      :memory-in-g-b="memoryInGB"
      :number-c-p-us="preferences?.virtualMachine?.cpus ?? 1"
      :avail-memory-in-g-b="availMemoryInGB"
      :avail-num-c-p-us="availNumCPUs"
      :reserved-memory-in-g-b="6"
      :reserved-num-c-p-us="1"
      :is-locked-memory="isPreferenceLocked('virtualMachine.memory')"
      :is-locked-cpu="isPreferenceLocked('virtualMachine.cpus')"
      @update:memory="setMemory($event)"
      @update:cpu="setCPUs($event)"
    />
  </div>
</template>
