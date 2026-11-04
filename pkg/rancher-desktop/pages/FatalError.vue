<!--
  -- SPDX-License-Identifier: Apache-2.0
  -- SPDX-FileCopyrightText: SUSE LLC
  -- SPDX-FileCopyrightText: The Rancher Desktop Authors
  -->

<template>
  <div class="container">
    <div class="error-header">
      <img
        id="logo"
        src="@/resources/icons/logo-square-red@2x.png"
      >
      <h2
        id="header-title"
        v-text="t('fatalError.title')"
      />
      <h5
        id="header-version"
        v-clean-html="t('fatalError.version', { version, os, arch })"
      />
    </div>
    <div class="error-body">
      <h4
        id="message-title"
        v-text="titlePart"
      />
      <p
        id="message-body"
        v-text="mainMessage"
      />
      <details v-if="failureDetails">
        <summary v-text="t('fatalError.details')" />
        <pre v-text="failureDetails" />
      </details>
    </div>
    <div class="button-bar">
      <button
        class="role-secondary"
        @click="showLogs"
        v-text="t('fatalError.showLogs')"
      />
      <button
        v-focus
        class="role-primary"
        @click="close"
        v-text="t('generic.close')"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useStore } from 'vuex';

import { ipcRenderer } from '@pkg/utils/ipcRenderer';

defineOptions({
  name:   'fatal-error-dialog',
  layout: 'dialog',
});

const store = useStore();
const t = computed(() => store.getters['i18n/t']);
const titlePart = ref(t.value('fatalError.titleUnknown'));
const mainMessage = ref(t.value('fatalError.messageUnknown'));
const failureDetails = ref('');

// We can't use destructuring here, because these come from webpack's
// DefinePlugin which just does a textual replacement at build time.
const version = process.env.RD_VERSION;
const os = process.env.RD_OS;
const arch = process.env.RD_ARCH;

ipcRenderer.on('dialog/populate', (_event, { title, message, details }) => {
  titlePart.value = title;
  mainMessage.value = message;
  failureDetails.value = details ?? '';
});

function showLogs() {
  ipcRenderer.send('error/show-logs');
}

function close() {
  window.close();
}
</script>
<style lang="scss" scoped>
  .container {
    width: max-content;
    max-width: 52rem;
  }

  .error-header {
    display: grid;
    grid-template:
      "logo title"
      "logo version"
      / auto minmax(max-content, 1fr);
    column-gap: 0.75rem;
    #logo {
      grid-area: logo;
      height: 32px;
      width: 32px;
    }
    #header-title {
      grid-area: title;
      margin: 0.25rem 0;
    }
    #header-version {
      grid-area: version;
    }
  }
  .error-body {
    display: flex;
    flex-grow: 1;
    flex-flow: column;
    #message-title {
      margin-top: auto;
    }
    #message-body {
      background: var(--box-bg);
      border-radius: var(--border-radius);
      margin: 5px;
      min-width: 80vw; /* 80% of viewport-width as specified in createWindow() in window/index.ts */
      max-height: 20lh;
      overflow-y: auto;
      padding: 10px;
      white-space: pre-line;
    }
  }
  .button-bar {
    display: flex;
    justify-content: space-between;
  }
</style>
