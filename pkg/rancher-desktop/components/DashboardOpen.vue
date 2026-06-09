<script lang="ts">
import { defineComponent } from 'vue';

import { mapTypedGetters, mapTypedState } from '@pkg/entry/store';

export default defineComponent({
  name:     'dashboard-open',
  computed: {
    ...mapTypedGetters('rdd', ['app', 'status']),
    ...mapTypedState('steve', ['port']),
    kubernetesEnabled(): boolean {
      return !!this.app?.spec?.kubernetes?.enabled;
    },
    dashboardReady(): boolean {
      return this.status('KubernetesReady') && this.port > 0;
    },
  },
  methods: {
    openDashboard() {
      this.$emit('open-dashboard');
    },
  },
});
</script>

<template>
  <button
    v-if="kubernetesEnabled"
    :disabled="!dashboardReady"
    class="btn role-secondary btn-icon-text"
    @click="openDashboard"
  >
    {{ t('nav.userMenu.clusterDashboard') }}
  </button>
</template>
