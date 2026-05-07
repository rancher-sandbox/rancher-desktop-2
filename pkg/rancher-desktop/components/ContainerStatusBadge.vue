<template>
  <badge-state
    v-if="currentContainer"
    v-tooltip="{
      content: actionError,
    }"
    :color="isRunning ? 'bg-success' : 'bg-darker'"
    :icon="actionError ? 'icon-error' : ''"
    :label="containerState"
    data-testid="container-state"
  />
</template>

<script lang="ts">
import { BadgeState } from '@rancher/components';
import { defineComponent, PropType } from 'vue';

import { mapTypedGetters } from '@pkg/entry/store';

import type { IoRancherdesktopContainersV1alpha1Container } from '@rdd-client';

export default defineComponent({
  name:       'ContainerStatusBadge',
  components: { BadgeState },
  props:      {
    container: {
      type:    Object as PropType<IoRancherdesktopContainersV1alpha1Container | undefined>,
      default: undefined,
    },
  },
  computed:   {
    ...mapTypedGetters('container-engine', ['containerById']),
    containerId() {
      const { id } = this.$route.params;
      return Array.isArray(id) ? id[0] : id;
    },
    currentContainer() {
      return this.container ?? this.containerById(this.containerId) ?? null;
    },
    containerState(): string {
      return this.currentContainer?.status?.status || 'unknown';
    },
    actionError(): string | undefined {
      return this.currentContainer?.status?.lastAction?.state === 'Failed'
        ? this.currentContainer.status.lastAction.error
        : undefined;
    },
    isRunning(): boolean {
      return this.containerState === 'running';
    },
  },
});
</script>

<style lang="scss" scoped>
.badge-state :deep(.icon) {
  vertical-align: middle;
}
</style>
