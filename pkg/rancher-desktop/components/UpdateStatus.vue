<template>
  <div>
    <div class="version">
      <version />
      <rd-checkbox
        v-if="updatePossible"
        :preference="preference"
        :immediate="true"
        class="updatesEnabled"
        label="Check for updates automatically"
      />
    </div>
    <card
      v-if="hasUpdate"
      ref="updateInfo"
      :show-highlight-border="false"
    >
      <template #title>
        <div class="type-title">
          <h3>Update Available</h3>
        </div>
      </template>
      <template #body>
        <div ref="updateStatus">
          <p>
            {{ statusMessage }}
          </p>
          <p
            v-if="updateReady"
            class="update-notification"
          >
            Restart the application to apply the update.
          </p>
        </div>
        <details
          v-if="detailsMessage"
          class="release-notes"
        >
          <summary>Release Notes</summary>
          <div
            ref="releaseNotes"
            v-html="detailsMessage"
          />
        </details>
      </template>
      <template #actions>
        <button
          v-if="updateReady"
          ref="applyButton"
          class="btn role-secondary"
          :disabled="applying"
          @click="applyUpdate"
        >
          {{ applyMessage }}
        </button>
        <span v-else />
      </template>
    </card>
    <card
      v-else-if="unsupportedUpdateAvailable"
      :show-highlight-border="false"
    >
      <template #title>
        <div class="type-title">
          <h3>Latest Version Not Supported</h3>
        </div>
      </template>
      <template #body>
        <p>
          A newer version of Rancher Desktop is available, but not supported on your system.
        </p>
        <br>
        <p>
          For more information please see
          <a href="https://docs.rancherdesktop.io/getting-started/installation">the installation documentation</a>.
        </p>
      </template>
      <template #actions>
        <div />
      </template>
    </card>
  </div>
</template>

<script lang="ts" setup>
import * as Components from '@rancher/components';
import DOMPurify from 'dompurify';
import _ from 'lodash';
import { marked } from 'marked';
import { computed, PropType, ref } from 'vue';
import { useStore } from 'vuex';

import Version from '@pkg/components/Version.vue';
import RdCheckbox from '@pkg/components/form/RdCheckbox.vue';
import { UpdateState } from '@pkg/main/update';
import type { RecursiveLeafKeysOfType } from '@pkg/utils/typeUtils';

import type { IoRancherdesktopAppV1alpha1AppSpec as AppSpec } from '@rdd-client';

const { Card } = (Components as any).default ?? Components;

defineOptions({
  name: 'update-status',
});

const emit = defineEmits<{
  apply: [],
}>();

const { preference, updateState, locale } = defineProps({
  preference: {
    type:     String as PropType<RecursiveLeafKeysOfType<AppSpec, boolean | undefined>>,
    required: true,
  },
  updateState: {
    type:    Object as PropType<UpdateState | null>,
    default: null,
  },
  locale: {
    type:    String,
    default: undefined,
  },
});

const store = useStore();

const applying = ref(false);
const preferences = computed(() => store.getters['preferences/preferences']);
const updatesEnabled = computed(() => !!_.get(preferences.value, preference, false));
const updatePossible = computed(() => !!updateState?.configured);
const hasUpdate = computed(() => updatesEnabled.value && !!updateState?.available);
const updateReady = computed(() => hasUpdate.value && !!updateState?.downloaded && !updateState?.error);

const statusMessage = computed(() => {
  if (updateState?.error) {
    return 'There was an error checking for updates.';
  }
  if (!updateState?.info) {
    return '';
  }

  const { info, progress } = updateState;
  const prefix = `An update to version ${ info.version } is available`;

  if (!progress) {
    return `${ prefix }.`;
  }

  const percent = Math.floor(progress.percent);
  const speed = Intl.NumberFormat(locale, {
    style:       'unit',
    unit:        'byte-per-second',
    unitDisplay: 'narrow',
    notation:    'compact',
  }).format(progress.bytesPerSecond);

  return `${ prefix }; downloading... (${ percent }%, ${ speed })`;
});

const detailsMessage = computed(() => {
  const markdown = updateState?.info?.releaseNotes;

  if (typeof markdown !== 'string') {
    return undefined;
  }

  const unsanitized = marked(markdown, { async: false });

  return DOMPurify.sanitize(unsanitized, { USE_PROFILES: { html: true } });
});

const applyMessage = computed(() => applying.value ? 'Applying update...' : 'Restart Now');

const unsupportedUpdateAvailable = computed(() => !hasUpdate.value && !!updateState?.info?.unsupportedUpdateAvailable);

function applyUpdate() {
  applying.value = true;
  emit('apply');
}
</script>

<style lang="scss" scoped>
  .version {
    display: flex;
    justify-content: space-between
  }
  .update-notification {
    font-weight: 900;
  }
  .release-notes > summary {
    margin: 1em;
  }
  .release-notes > div {
    margin-left: 2em;
    margin-right: 1em;
  }
</style>

<style lang="scss">
  .release-notes p {
    margin: 1em 0px;
  }
</style>
