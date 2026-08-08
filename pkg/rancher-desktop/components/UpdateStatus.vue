<template>
  <div class="update-status">
    <template v-if="hasUpdate">
      <h3>{{ t('updateStatus.updateAvailable') }}</h3>
      <card
        ref="updateInfo"
        :show-highlight-border="false"
      >
        <template #body>
          <div ref="updateStatus">
            <p>
              {{ statusMessage }}
            </p>
            <p
              v-if="updateReady"
              class="update-notification"
            >
              {{ t('updateStatus.restartToApply') }}
            </p>
          </div>
          <details
            v-if="detailsMessage"
            class="release-notes"
          >
            <summary>{{ t('updateStatus.releaseNotes') }}</summary>
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
    </template>
    <template v-else-if="unsupportedUpdateAvailable">
      <h3>{{ t('updateStatus.unsupported.title') }}</h3>
      <card :show-highlight-border="false">
        <template #body>
          <p>
            {{ t('updateStatus.unsupported.message') }}
          </p>
          <br>
          <!-- v-clean-html: the translated string embeds a link -->
          <p v-clean-html="t('updateStatus.unsupported.seeDocumentation')" />
        </template>
        <template #actions>
          <div />
        </template>
      </card>
    </template>
  </div>
</template>

<script lang="ts" setup>
import * as Components from '@rancher/components';
import DOMPurify from 'dompurify';
import _ from 'lodash';
import { marked } from 'marked';
import { computed, PropType, ref } from 'vue';
import { useStore } from 'vuex';

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

const t = computed(() => store.getters['i18n/t']);
const applying = ref(false);
const preferences = computed(() => store.getters['preferences/preferences']);
const updatesEnabled = computed(() => !!_.get(preferences.value, preference, false));
const hasUpdate = computed(() => updatesEnabled.value && !!updateState?.available);
const updateReady = computed(() => hasUpdate.value && !!updateState?.downloaded && !updateState?.error);

const statusMessage = computed(() => {
  if (updateState?.error) {
    return t.value('updateStatus.errorChecking');
  }
  if (!updateState?.info) {
    return '';
  }

  const { info, progress } = updateState;

  if (!progress) {
    return t.value('updateStatus.available', { version: info.version });
  }

  const percent = Math.floor(progress.percent);
  const speed = Intl.NumberFormat(locale, {
    style:       'unit',
    unit:        'byte-per-second',
    unitDisplay: 'narrow',
    notation:    'compact',
  }).format(progress.bytesPerSecond);

  return t.value('updateStatus.downloading', { version: info.version, percent: String(percent), speed });
});

const detailsMessage = computed(() => {
  const markdown = updateState?.info?.releaseNotes;

  if (typeof markdown !== 'string') {
    return undefined;
  }

  const unsanitized = marked(markdown, { async: false });

  return DOMPurify.sanitize(unsanitized, { USE_PROFILES: { html: true } });
});

const applyMessage = computed(() =>
  applying.value
    ? t.value('updateStatus.applyingUpdate')
    : t.value('updateStatus.restartNow'));

const unsupportedUpdateAvailable = computed(() =>
  !hasUpdate.value && !!updateState?.info?.unsupportedUpdateAvailable);

function applyUpdate() {
  applying.value = true;
  emit('apply');
}
</script>

<style lang="scss" scoped>
  // Shrink so long release notes scroll inside the card, not push the blog off.
  .update-status {
    display: flex;
    flex-direction: column;
    min-height: 0;

    // Match the blog feed's heading above its box.
    h3 {
      margin-bottom: 0.75rem;
    }
  }

  // Keep the card tall enough to read the notes once they are open.
  .update-status:has(.release-notes[open]) {
    min-height: 14rem;
  }

  :deep(.card-container) {
    // Drop the Card's grid margin so the box aligns with the blog box.
    margin-left: 0;
    margin-right: 0;
    // Fill and shrink past the Card's 100px minimum, so the body scrolls.
    flex: 1;
    min-height: 0;
  }

  // Hide the empty title and <hr> the Card draws with no title slot.
  :deep(.card-title),
  :deep(.card-wrap > hr) {
    display: none;
  }

  // card-wrap is a plain block here; make it a column so the body can scroll.
  :deep(.card-wrap) {
    display: flex;
    flex-direction: column;
    min-height: 0;
  }

  // Scroll long notes inside the card; anchor to the top (the Card centres it).
  :deep(.card-body) {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    justify-content: flex-start;
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
