<script lang="ts" setup>
import { computed } from 'vue';
import { useStore } from 'vuex';

import PreferencesAlert from '@pkg/components/Preferences/Alert.vue';

defineOptions({ name: 'preferences-footer' });

const emit = defineEmits<{
  apply:  [],
  cancel: [],
}>();

const store = useStore();
const canApply = computed(() => store.getters['preferences/canApply']);
const errorStatus = computed(() => store.state.preferences.errorStatus);

function cancel() {
  emit('cancel');
}

function apply() {
  emit('apply');
}
</script>

<template>
  <div class="preferences-footer">
    <div class="preferences-alert">
      <preferences-alert
        v-if="errorStatus"
      />
    </div>
    <div class="preferences-actions">
      <button
        data-test="preferences-cancel"
        class="btn role-secondary"
        @click="cancel"
      >
        {{ t('generic.cancel') }}
      </button>
      <button
        data-testid="preferences-apply"
        class="btn role-primary"
        :disabled="!canApply"
        @click="apply"
      >
        {{ t('generic.apply') }}
      </button>
    </div>
  </div>
</template>

<style lang="scss" scoped>
  .preferences-footer {
    display: flex;
    justify-content: space-between;
    border-top: 1px solid var(--header-border);
    padding: var(--preferences-content-padding);

    .preferences-alert {
      display: flex;
      justify-content: right;
      align-items: center;
      height: 101%;
      width: 100%;
      padding-right: var(--preferences-content-padding);
    }

    .preferences-actions {
      display: flex;
      justify-content: flex-end;
      gap: 1rem;
    }
  }
</style>
