<template>
  <div class="first-run-container">
    <preferences-alert v-if="errorStatus" />
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
    <rd-fieldset
      :legend-text="t('pathManagement.label')"
      :legend-tooltip="t('pathManagement.tooltip', { binDir: escapeHtml(binDir) })"
      :is-locked="isPreferenceLocked('application.addPath')"
    >
      <path-management-selector
        :show-label="false"
        :disabled="!preferencesLoaded"
      />
    </rd-fieldset>
    <div class="button-area">
      <button
        ref="accept-btn"
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
import { computed, inject, onBeforeMount, onBeforeUnmount, ref, useTemplateRef, watch } from 'vue';
import { useStore } from 'vuex';

import EngineSelector from '@pkg/components/EngineSelector.vue';
import KubernetesVersionSelector from '@pkg/components/KubernetesVersionSelector.vue';
import PathManagementSelector from '@pkg/components/PathManagementSelector.vue';
import PreferencesAlert from '@pkg/components/Preferences/Alert.vue';
import RdCheckbox from '@pkg/components/form/RdCheckbox.vue';
import RdFieldset from '@pkg/components/form/RdFieldset.vue';
import { binDirInjectionKey } from '@pkg/types/injectionKeys';
import { escapeHtml } from '@pkg/utils/string';

defineOptions({ name: 'first-run-dialog', layout: 'dialog' });

const store = useStore();
const t = computed(() => store.getters['i18n/t']);
const preferences = computed(() => store.getters['preferences/preferences']);
const isPreferenceLocked = computed(() => store.getters['preferences/isPreferenceLocked']);
const engineSelectorLocked = computed(() => isPreferenceLocked.value('containerEngine.name'));
const binDir = inject(binDirInjectionKey, ref('~/.rd2/bin'));
const preferencesLoaded = computed(() => Object.keys(preferences.value ?? {}).length > 0);
const processing = ref(false);
const errorStatus = computed(() => store.state.preferences.errorStatus);
const acceptBtn = useTemplateRef('accept-btn');

// Focus the accept button once preferences are loaded (and the controls are
// enabled).
const unwatchPreferencesLoaded = watch(preferencesLoaded, (newValue) => {
  if (newValue && acceptBtn.value) {
    unwatchPreferencesLoaded();
    acceptBtn.value.focus();
  }
}, { flush: 'post', immediate: true });

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
