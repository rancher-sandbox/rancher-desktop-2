<!-- This is the Kubernetes backend progress notification in the bottom left
   - corner of the default layout.
   -->

<template>
  <div
    v-if="!settled"
    class="progress"
  >
    <label
      class="details"
      :title="description"
    >{{ description }}</label>
    <RdProgress
      class="progress-bar"
      :indeterminate="true"
    />
  </div>
</template>

<script lang="ts" setup>
import { computed } from 'vue';
import { useStore } from 'vuex';

import RdProgress from '@pkg/components/RdProgress.vue';

defineOptions({ name: 'backend-progress' });

const store = useStore();

/** Whether the backend is settled. */
const settled = computed(() => store.getters['rdd/settled']);
const app = computed(() => store.getters['rdd/app']);
const conditions = computed(() => app.value?.status?.conditions ?? []);
/** Conditions where the status is not True. */
const pending = computed(() => conditions.value.filter(c => c.status !== 'True'));
/** The newest transition time of the pending conditions. */
const newestTransitionTime = computed(() => {
  // This uses a `Date` instead of `.valueOf()`, so that when debugging we can
  // read the date easier.
  return pending.value.reduce((latest, c) => {
    return latest.valueOf() > c.lastTransitionTime.valueOf() ? latest : c.lastTransitionTime;
  }, new Date(0));
});
/** Conditions where the status is not True, only if the transition time is the latest. */
const latestTransitions = computed(() => {
  return pending.value.filter(c => {
    return c.lastTransitionTime.valueOf() === newestTransitionTime.value.valueOf();
  });
});
const selectedTransition = computed(() => {
  // If there are multiple conditions with the same transition time, hard-code
  // the preference order.
  const order = ['Created', 'Running', 'ContainerEngineReady', 'KubernetesReady', 'Settled'];
  const preferredType = order.find(t => latestTransitions.value.some(c => c.type === t));
  const preferred = preferredType ? latestTransitions.value.find(c => c.type === preferredType) : undefined;

  return preferred ?? latestTransitions.value[0];
});

const description = computed(() => {
  const transition = selectedTransition.value;
  // If the app doesn't exist yet (e.g. because we're still starting the RDD
  // service), print a fallback message.

  return transition?.message || transition?.reason || 'Starting control plane';
});
</script>

<style lang="scss" scoped>
  .progress {
    display: flex;
    flex-direction: row;
    white-space: nowrap;
    align-items: center;
    flex: 1;

    .details {
      text-align: end;
      text-overflow: ellipsis;
      overflow: hidden;
      padding-right: 0.25rem;
      flex: 1;
    }

    .progress-bar {
      max-width: 12rem;
    }
  }
</style>
