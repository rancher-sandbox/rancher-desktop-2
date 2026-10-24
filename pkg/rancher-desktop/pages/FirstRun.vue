<template>
  <div class="first-run-container">
    <h2 data-test="k8s-settings-header">
      {{ t('firstRun.welcome') }}
    </h2>
    <rd-checkbox
      data-testid="kubernetes-enabled"
      :label="t('firstRun.enableKubernetes')"
      preference="kubernetes.enabled"
    />
    <kubernetes-version-selector data-testid="kubernetes-version" />
    <rd-fieldset
      :legend-text="t('containerEngine.label')"
      :is-locked="engineSelectorLocked"
    >
      <engine-selector
        data-testid="engine-selector"
        :disabled="!preferencesLoaded"
      />
    </rd-fieldset>
    <!--
    <rd-fieldset
      v-if="pathManagementRelevant"
      :legend-text="t('pathManagement.label')"
      :legend-tooltip="t('pathManagement.tooltip')"
      :is-locked="pathManagementSelectorLocked"
    >
      <path-management-selector
        :value="pathManagementStrategy"
        :is-locked="pathManagementSelectorLocked"
        :show-label="false"
        @input="setPathManagementStrategy"
      />
    </rd-fieldset>
    -->
    <div class="button-area">
      <button
        v-focus
        data-testid="accept-btn"
        class="role-primary"
        :disabled="!preferencesLoaded || processing"
        @click="commitAndClose"
      >
        {{ t('firstRun.ok') }}
      </button>
    </div>
  </div>
</template>

<script lang="ts" setup>
import { computed, onBeforeMount, onBeforeUnmount, onMounted, ref } from 'vue';
import { useStore } from 'vuex';

import EngineSelector from '@pkg/components/EngineSelector.vue';
import KubernetesVersionSelector from '@pkg/components/KubernetesVersionSelector.vue';
import RdCheckbox from '@pkg/components/form/RdCheckbox.vue';
import RdFieldset from '@pkg/components/form/RdFieldset.vue';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';

defineOptions({ name: 'first-run-dialog', layout: 'dialog' });

const store = useStore();
const t = computed(() => store.getters['i18n/t']);
const preferences = computed(() => store.getters['preferences/preferences']);
const isPreferencesLocked = computed(() => store.getters['preferences/isPreferenceLocked']);
const engineSelectorLocked = computed(() => isPreferencesLocked.value('containerEngine.name'));
const preferencesLoaded = computed(() => Object.keys(preferences.value ?? {}).length > 0);
const processing = ref(false);

async function commitAndClose(event: Event) {
  event.preventDefault();
  processing.value = true;
  let committed = false;
  try {
    committed = await store.dispatch('preferences/commit');
  } catch (ex) {
    console.error(ex);
  }
  // Close the window if we managed to commit, or if the user is just trying to
  // close the window.  In the latter case, we'll end up opening the main
  // window, which will then display any errors via the normal code path.
  if (committed || event.type === 'beforeunload') {
    window.removeEventListener('beforeunload', commitAndClose);
    window.close();
  } else {
    // If we're not closing the window, let the user try clicking again.
    processing.value = false;
  }
}

// Save default settings on closing window.
onBeforeMount(async() => {
  try {
    window.addEventListener('beforeunload', commitAndClose);
    await store.dispatch('rdd-connection/fetchConfig');
    await store.dispatch('rdd/watchResources', ['apps']);
    await store.dispatch('rdd/ensureAppExists');
  } catch (ex) {
    console.error(ex);
  }
});

onMounted(() => {
  ipcRenderer.send('dialog/ready');
});

onBeforeUnmount(async() => {
  try {
    await store.dispatch('rdd/unwatchResources', ['apps']);
  } catch (ex) {
    console.error(ex);
  }
});
</script>

<style lang="scss">
  html {
    height: initial;
  }
</style>

<style lang="scss" scoped>
  .first-run-container {
    display: flex;
    flex-direction: column;

    & > * {
      contain: inline-size;
      width: 100%;
    }

    h2 {
      contain: initial;
      min-width: 26rem;
    }

    .button-area {
      display: flex;
      flex-direction: row-reverse;
    }
  }
</style>
