<script lang="ts" setup>
import { computed } from 'vue';
import { useStore } from 'vuex';

import RdSelect from '@pkg/components/RdSelect.vue';
import RdCheckbox from '@pkg/components/form/RdCheckbox.vue';
import RdFieldset from '@pkg/components/form/RdFieldset.vue';

defineOptions({ name: 'preferences-application-general' });

const store = useStore();
const isPreferenceLocked = computed(() => store.getters['preferences/isPreferenceLocked']);
const availableLocales = computed(() => store.getters['i18n/availableLocales']);
const selectedLocale = computed(() => store.getters['i18n/current']);
const showLocaleDisclaimer = computed(() => selectedLocale.value !== 'en-us');

function onLocaleChange(newLocale: string) {
  store.dispatch('preferences/modify', { key: 'application.locale', value: newLocale });
}
</script>

<template>
  <div class="application-general">
    <rd-fieldset
      data-test="locale"
      class="width-xs"
      :legend-text="t('application.locale.legendText')"
      :is-experimental="true"
    >
      <rd-select
        data-test="localeSelect"
        :model-value="selectedLocale"
        :aria-label="t('application.locale.legendText')"
        :is-locked="isPreferenceLocked('application.locale')"
        @input="onLocaleChange"
      >
        <option
          v-for="(label, code) in availableLocales"
          :key="code"
          :value="code"
        >
          {{ label }}
        </option>
      </rd-select>
      <p
        v-if="showLocaleDisclaimer"
        class="locale-disclaimer"
      >
        {{ t('application.locale.disclaimer') }}
      </p>
    </rd-fieldset>
    <!-- We don't have sudo access at this point
    <rd-fieldset
      v-if="platform !== 'win32'"
      data-test="administrativeAccess"
      :legend-text="t('application.general.adminAccess.legendText')"
      :legend-tooltip="t('application.general.adminAccess.legendTooltip')"
    >
      <rd-checkbox
        :label="t('application.general.adminAccess.label')"
        :value="isSudoAllowed"
        :is-locked="isPreferenceLocked('application.adminAccess')"
        @update:value="onChange('application.adminAccess', $event)"
      />
    </rd-fieldset>
  -->
    <rd-fieldset
      data-test="automaticUpdates"
      :legend-text="t('application.general.automaticUpdates.legendText')"
    >
      <rd-checkbox
        preference="application.updates.enabled"
        data-test="automaticUpdatesCheckbox"
        :label="t('application.general.automaticUpdates.label')"
      />
    </rd-fieldset>
    <!--
    <rd-fieldset
      data-test="statistics"
      :legend-text="t('application.general.statistics.legendText')"
    >
      <rd-checkbox
        preference="application.telemetry.enabled"
        :label="t('application.general.statistics.label')"
      />
    </rd-fieldset>
    -->
  </div>
</template>

<style lang="scss" scoped>
  .application-general {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .width-xs {
    max-width: 20rem;
    min-width: 20rem;
  }

  .locale-disclaimer {
    margin-top: 0.5rem;
    font-size: 0.85rem;
    color: var(--input-label);
  }
</style>
