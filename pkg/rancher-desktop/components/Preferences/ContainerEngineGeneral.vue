<script lang="ts" setup>
import { computed } from 'vue';
import { useStore } from 'vuex';

import EngineSelector from '@pkg/components/EngineSelector.vue';
import RdFieldset from '@pkg/components/form/RdFieldset.vue';

defineOptions({ name: 'preferences-container-engine-general' });

const store = useStore();
const isPreferenceLocked = computed(() => store.getters['preferences/isPreferenceLocked']);

</script>

<template>
  <div class="container-engine-general">
    <rd-fieldset
      data-testid="containerEngine"
      :legend-text="t('containerEngine.label')"
      :is-locked="isPreferenceLocked('containerEngine.name')"
    >
      <engine-selector />
    </rd-fieldset>
  </div>
</template>

<style lang="scss" scoped>
.container-engine-general {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
</style>
