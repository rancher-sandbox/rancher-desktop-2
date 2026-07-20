<script lang="ts" setup>
import semverCompare from 'semver/functions/compare';
import { computed, onBeforeMount, onBeforeUnmount } from 'vue';
import { useStore } from 'vuex';

import RdSelect from '@pkg/components/RdSelect.vue';
import RdCheckbox from '@pkg/components/form/RdCheckbox.vue';
import RdFieldset from '@pkg/components/form/RdFieldset.vue';

defineOptions({ name: 'preferences-body-kubernetes' });

const store = useStore();
const preferences = computed(() => store.getters['preferences/preferences']);
const cachedVersionsOnly = computed(() => store.state.network.offline);
const versions = computed(() => store.getters['kubernetes/k3sVersions']);
const channels = computed(() => store.getters['kubernetes/k3sChannels']);
/** The currently selected Kubernetes version. */
const selectedVersion = computed(() => {
  // If there is no version in the settings, try some fallbacks.
  const candidates = [
    preferences.value?.kubernetes?.version,
    channels.value?.stable,
    channels.value?.latest,
    ...Object.values(channels.value ?? {}).sort(semverCompareDesc),
    ...Object.keys(versions.value ?? {}).sort(semverCompareDesc),
  ];
  const fallback = preferences.value?.kubernetes?.version;
  return candidates.find(v => v && v in (versions.value ?? {})) ?? fallback;
});
/** recommendedVersions is a list of tuples, `[version, channels]` */
const recommendedVersions = computed(() => {
  const recommended = new Set(Object.values(channels.value ?? {}));
  return Object.keys(versions.value ?? {}).filter(v => recommended.has(v)).map(v => {
    const chs = Object.entries(channels.value ?? {})
      .filter(([_, ver]) => v === ver)
      .map(([ch]) => ch)
      // Drop all vaguely version-like channels
      .filter(ch => !/^v?\d+\.\d+/.test(ch));
    return [v, chs] as const;
  }).sort((a, b) => semverCompareDesc(a[0], b[0]));
});
const nonRecommendedVersions = computed(() => {
  const recommended = new Set(Object.values(channels.value ?? {}));
  return Object.keys(versions.value ?? {})
    .filter(v => !recommended.has(v))
    .sort(semverCompareDesc);
});
const kubernetesVersionLabel = computed(() =>
  `Kubernetes version${ cachedVersionsOnly.value ? ' (cached versions only)' : '' }`);
const isKubernetesDisabled = computed(() => !preferences.value?.kubernetes?.enabled);
const isPreferenceLocked = computed(() => store.getters['preferences/isPreferenceLocked']);

function semverCompareDesc(a: string, b: string) {
  try {
    return semverCompare(b, a);
  } catch {
    return String(b).localeCompare(String(a));
  }
}

function formatRecommendedVersion(input: readonly [string, string[]]) {
  const [version, channels] = input;
  if (channels.length === 0) {
    return version;
  }
  return `${ version } (${ channels.join(', ') })`;
}

function onVersionChanged(value: string) {
  store.dispatch('preferences/modify', { key: 'kubernetes.version', value });
}

onBeforeMount(() => {
  store.dispatch('kubernetes/watchResources', ['configMaps']);
});
onBeforeUnmount(() => {
  store.dispatch('kubernetes/unwatchResources', ['configMaps']);
});

</script>

<template>
  <div class="preferences-body">
    <rd-fieldset
      data-test="kubernetesToggle"
      legend-text="Kubernetes"
    >
      <rd-checkbox
        label="Enable Kubernetes"
        preference="kubernetes.enabled"
      />
    </rd-fieldset>
    <rd-fieldset
      data-test="kubernetesVersion"
      class="width-xs"
      :legend-text="kubernetesVersionLabel"
    >
      <rd-select
        class="select-k8s-version"
        :model-value="selectedVersion"
        :disabled="isKubernetesDisabled"
        :is-locked="isPreferenceLocked('kubernetes.version')"
        @change="onVersionChanged($event.target.value)"
      >
        <!--
          - On macOS Chrome / Electron can't style the <option> elements.
          - We do the best we can by instead using <optgroup> for a recommended section.
          -->
        <optgroup
          v-if="recommendedVersions.length > 0"
          label="Recommended Versions"
        >
          <option
            v-for="item in recommendedVersions"
            :key="item[0]"
            :value="item[0]"
            :selected="item[0] === selectedVersion"
          >
            {{ formatRecommendedVersion(item) }}
          </option>
        </optgroup>
        <optgroup
          v-if="nonRecommendedVersions.length > 0"
          label="Other Versions"
        >
          <option
            v-for="item in nonRecommendedVersions"
            :key="item"
            :value="item"
            :selected="item === selectedVersion"
          >
            {{ item }}
          </option>
        </optgroup>
      </rd-select>
    </rd-fieldset>
    <!--
    <rd-fieldset
      data-test="kubernetesOptions"
      legend-text="Options"
    >
      <rd-checkbox
        label="Enable Traefik"
        :disabled="isKubernetesDisabled"
        :value="preferences.kubernetes.options.traefik"
        :is-locked="isPreferenceLocked('kubernetes.options.traefik')"
        @update:value="onChange('kubernetes.options.traefik', $event)"
      />
    -->
    <!-- Don't disable Spinkube option when Wasm is disabled; let validation deal with it  -->
    <!--
      <rd-checkbox
        label="Install Spin Operator"
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
          <banner color="warning">
            Spin operator requires
            <a
              href="#"
              @click.prevent="($root as any).navigate('Container Engine', 'general')"
            >WebAssembly</a>
            to be enabled.
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
