<script lang="ts" setup>

import { computed, onBeforeMount, onBeforeUnmount, Transition } from 'vue';
import { useStore } from 'vuex';

import EmptyState from '@pkg/components/EmptyState.vue';
import PreferencesBody from '@pkg/components/Preferences/ModalBody.vue';
import PreferencesFooter from '@pkg/components/Preferences/ModalFooter.vue';
import PreferencesHeader from '@pkg/components/Preferences/ModalHeader.vue';
import PreferencesNav from '@pkg/components/Preferences/ModalNav.vue';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';
import { Direction } from '@pkg/utils/typeUtils';
import { preferencesNavItemName, preferencesNavItems } from '@pkg/window/preferenceConstants';

defineOptions({
  name:   'preferences-modal',
  layout: 'preferences',
});

const store = useStore();

const navigation = computed(() => store.state['transient-preferences'].navigation);
const committed = computed(() => store.getters['preferences/committed']);
const hasPreferences = computed(() => Object.keys(committed.value).length > 0);
const navItems = computed(() => Object.keys(preferencesNavItems) as preferencesNavItemName[]);
const currentNavItem = computed(() => navigation.value.preferences.top);

async function navChanged(current: preferencesNavItemName) {
  await store.dispatch('transient-preferences/navigate', { 'preferences.top': current });
}
async function closePreferences() {
  // Clear any uncommitted changes, so the next time we open the window we
  // reset any modifications.
  await store.dispatch('preferences/clear');
  window.close();
}
async function applyPreferences() {
  if (await store.dispatch('preferences/commit')) {
    window.close();
  }
}
async function navigateToTab(_event: Electron.IpcRendererEvent, args: { name?: preferencesNavItemName, direction?: Direction }) {
  const { name, direction } = args;

  if (name) {
    await store.dispatch('transient-preferences/navigate', { 'preferences.top': name });

    return;
  }

  if (direction) {
    const dir = (direction === 'forward' ? 1 : -1);
    const idx = (navItems.value.length + navItems.value.indexOf(currentNavItem.value) + dir) % navItems.value.length;

    await store.dispatch('transient-preferences/navigate', { 'preferences.top': navItems.value[idx] });
  }
}

onBeforeMount(() => {
  store.dispatch('rdd/watchResources', ['apps']);
  ipcRenderer.on('route', navigateToTab);
});
onBeforeUnmount(() => {
  ipcRenderer.removeListener('route', navigateToTab);
  store.dispatch('rdd/unwatchResources', ['apps']);
  store.dispatch('preferences/clear');
});
</script>

<!--
  To make the components easier, we show an error to the user until the
  preferences have been loaded.  This way the components that actually use the
  preferences (i.e. nav bar, body) can assume it is fully loaded.
-->
<template>
  <div class="modal-grid">
    <preferences-header
      class="preferences-header"
    />
    <transition
      v-if="!hasPreferences"
      name="empty-state-fade"
      appear
    >
      <!-- Use a fade transition to avoid a flash on initial load. -->
      <div class="preferences-error">
        <empty-state
          icon="icon-warning"
          heading="Unable to fetch preferences"
          body="Reopen the window to try again."
        />
      </div>
    </transition>
    <preferences-nav
      v-if="hasPreferences"
      class="preferences-nav"
      :current-nav-item="currentNavItem"
      :nav-items="navItems"
      @nav-changed="navChanged"
    />
    <preferences-body
      v-if="hasPreferences"
      v-bind="$attrs"
      class="preferences-body"
      :current-nav-item="currentNavItem"
    />
    <preferences-footer
      class="preferences-footer"
      @cancel="closePreferences"
      @apply="applyPreferences"
    />
  </div>
</template>

<style lang="scss">
  .modal .vm--modal {
    background-color: var(--body-bg);
  }

  .preferences-header {
    grid-area: header;
  }

  .preferences-nav {
    grid-area: nav;
  }

  .preferences-body {
    grid-area: body;
    max-height: 100%;
    overflow: auto;
  }

  .preferences-footer {
    grid-area: footer;
  }

  .modal-grid {
    height: 100vh;
    display: grid;
    grid-template-columns: auto 1fr;
    grid-template-rows: auto 1fr auto;
    grid-template-areas:
      "header header"
      "nav body"
      "footer footer";
  }

  .preferences-error {
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: row;
    grid-column-start: span 2;
    justify-content: center;
    align-items: center;
    padding-bottom: 6rem;
  }

  /* Set up the fade transition for the no-prefs error screen. */
  .empty-state-fade-enter-from {
    opacity: 0;
  }
  .empty-state-fade-enter-to {
    opacity: 1;
  }
  .empty-state-fade-enter-active {
    transition: opacity 0.5s;
  }
</style>
