<script lang="ts" setup>
import { computed } from 'vue';
import { useStore } from 'vuex';

import KubernetesVersionSelector from '@pkg/components/KubernetesVersionSelector.vue';
import RdCheckbox from '@pkg/components/form/RdCheckbox.vue';
import RdFieldset from '@pkg/components/form/RdFieldset.vue';

defineOptions({ name: 'preferences-body-kubernetes' });

const store = useStore();
const t = computed(() => store.getters['i18n/t']);

</script>

<template>
  <div class="preferences-body">
    <rd-fieldset
      data-test="kubernetesToggle"
      :legend-text="t('kubernetesPrefs.kubernetes.legendText')"
    >
      <rd-checkbox
        :label="t('kubernetesPrefs.kubernetes.label')"
        preference="kubernetes.enabled"
      />
    </rd-fieldset>
    <kubernetes-version-selector />
    <!--
    <rd-fieldset
      data-test="kubernetesOptions"
      :legend-text="t('kubernetesPrefs.options.legendText')"
    >
      <rd-checkbox
        :label="t('kubernetesPrefs.options.traefik')"
        :disabled="isKubernetesDisabled"
        :value="preferences.kubernetes.options.traefik"
        :is-locked="isPreferenceLocked('kubernetes.options.traefik')"
        @update:value="onChange('kubernetes.options.traefik', $event)"
      />
    -->
    <!-- Don't disable Spinkube option when Wasm is disabled; let validation deal with it  -->
    <!--
      <rd-checkbox
        :label="t('kubernetesPrefs.options.spinkube')"
        :disabled="isKubernetesDisabled"
        :value="preferences.experimental.kubernetes.options.spinkube"
        :is-locked="isPreferenceLocked('experimental.kubernetes.options.spinkube')"
        :is-experimental="true"
        @update:value="onChange('experimental.kubernetes.options.spinkube', $event)"
      >
        <template
          v-if="spinOperatorIncompatible"
          #below
        >
          <banner
            color="warning"
            @click.prevent="onWarningClick"
          >
            --><!-- v-clean-html: the translated warning embeds a link --><!--
            <span v-clean-html="t('kubernetesPrefs.options.spinkubeWarning')" />
          </banner>
        </template>
      </rd-checkbox>
    </rd-fieldset>
    -->
  </div>
</template>

<style lang="scss" scoped>
  .checkbox-title {
    font-size: 1rem;
    line-height: 1.5rem;
    padding-bottom: 0.5rem;
  }

  .preferences-body {
    padding: var(--preferences-content-padding);
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .width-xs {
    max-width: 20rem;
    min-width: 20rem;
  }
</style>
