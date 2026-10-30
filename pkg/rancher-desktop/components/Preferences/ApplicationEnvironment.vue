<script setup lang="ts">
import { computed, inject, ref } from 'vue';
import { useStore } from 'vuex';

import PathManagementSelector from '@pkg/components/PathManagementSelector.vue';
import RdFieldset from '@pkg/components/form/RdFieldset.vue';
import { binDirInjectionKey } from '@pkg/types/injectionKeys';
import { escapeHtml } from '@pkg/utils/string';

defineOptions({ name: 'preferences-application-environment' });

const store = useStore();
const t = computed(() => store.getters['i18n/t']);
const isPreferenceLocked = computed(() => store.getters['preferences/isPreferenceLocked']);
const binDir = inject(binDirInjectionKey, ref('~/.rd2/bin'));

</script>

<template>
  <rd-fieldset
    data-testid="pathManagement"
    :legend-text="t('pathManagement.label')"
    :legend-tooltip="t('pathManagement.tooltip', { binDir: escapeHtml(binDir) })"
    :is-locked="isPreferenceLocked('application.addPath')"
  >
    <path-management-selector :show-label="false" />
  </rd-fieldset>
</template>
