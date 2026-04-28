<template>
  <div
    class="container-info-page"
    data-testid="container-info"
  >
    <banner
      v-if="errorMessage"
      color="error"
      @close="clearError"
    >
      {{ errorMessage }}
    </banner>
    <rd-tabbed
      :key="containerId"
      :flat="true"
    >
      <!--
        Tab components are used only to register headers and emit @active events; their slots
        are intentionally empty. Content is rendered in .tab-content below so we can mix
        v-if (destroy/recreate on switch) with v-show (preserve the shell terminal's DOM and
        pty process across tab switches).
      -->
      <tab
        label="Logs"
        name="tab-logs"
        :weight="1"
        @active="activeTab = 'tab-logs'"
      />
      <!--
      <tab
        label="Shell"
        name="tab-shell"
        :weight="0"
        :disabled="!isRunning"
        @active="activeTab = 'tab-shell'"
      />
      -->
      <template #tab-row-extras>
        <li
          v-if="activeTab === 'tab-logs'"
          class="search-widget"
          data-testid="search-widget"
        >
          <input
            ref="searchInput"
            v-model="searchTerm"
            aria-label="Search in logs"
            class="search-input"
            data-testid="search-input"
            placeholder="Search logs..."
            type="search"
            @input="onSearchInput"
            @keydown="handleSearchKeydown"
          >
          <button
            :disabled="!searchTerm"
            aria-label="Previous match"
            class="search-btn btn role-tertiary"
            data-testid="search-prev-btn"
            title="Previous match"
            @click="searchPrevious"
          >
            <i
              aria-hidden="true"
              class="icon icon-chevron-up"
            />
          </button>
          <button
            :disabled="!searchTerm"
            aria-label="Next match"
            class="search-btn btn role-tertiary"
            data-testid="search-next-btn"
            title="Next match"
            @click="searchNext"
          >
            <i
              aria-hidden="true"
              class="icon icon-chevron-down"
            />
          </button>
          <button
            :disabled="!searchTerm"
            aria-label="Clear search"
            class="search-btn btn role-tertiary"
            data-testid="search-clear-btn"
            title="Clear search"
            @click="clearSearch"
          >
            <i
              aria-hidden="true"
              class="icon icon-x"
            />
          </button>
        </li>
      </template>
      <div class="tab-content">
        <container-logs
          v-if="containerId && activeTab === 'tab-logs'"
          ref="containerLogs"
          :container-id="containerId"
          :is-running="isRunning"
        />
        <!--
        TODO: Re-enabling this feature is filed as
        https://github.com/rancher-sandbox/rancher-desktop-app/issues/38
        <container-shell
          v-if="shellEverActivated && containerId"
          v-show="activeTab === 'tab-shell'"
          ref="containerShell"
          :container-id="containerId"
          :is-container-running="isRunning"
          :namespace="namespace"
        />
        -->
      </div>
    </rd-tabbed>
  </div>
</template>

<script setup lang="ts">
import { Banner } from '@rancher/components';
import { ref, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue';
import { useRoute } from 'vue-router';
import { useStore } from 'vuex';

import ContainerLogs from '@pkg/components/ContainerLogs.vue';
import ContainerShell from '@pkg/components/ContainerShell.vue';
import RdTabbed from '@pkg/components/Tabbed/RdTabbed.vue';
import Tab from '@pkg/components/Tabbed/Tab.vue';

// Router and Store
const route = useRoute();
const store = useStore();

// Template refs with proper typing
const containerLogs = ref<InstanceType<typeof ContainerLogs> | null>(null);
const containerShell = ref<InstanceType<typeof ContainerShell> | null>(null);
const searchInput = ref<HTMLInputElement | null>(null);

// Reactive data
const searchTerm = ref('');
const activeTab = ref<'tab-logs' | 'tab-shell'>('tab-logs');
const shellEverActivated = ref(false);

// Vuex integration
const supportsNamespaces = computed(() => store.getters['container-engine/supportsNamespaces']);
const namespace = computed(() => store.getters['container-engine/currentNamespace']);

// Computed properties
const error = computed(() => store.state['container-engine'].error);
const containerId = computed(() => route.params.id as string || '');

const currentContainer = computed(() => {
  return store.getters['container-engine/containerById'](containerId.value) ?? null;
});

const containerName = computed(() => {
  return currentContainer.value?.status?.name ?? containerId.value.substring(0, 12);
});

const isRunning = computed(() => {
  return currentContainer.value?.status?.status === 'running';
});

const errorMessage = computed(() => {
  if (['containers', 'namespaces'].includes(error.value?.source || '')) {
    return String(error.value?.error?.message ?? error.value?.error ?? error.value);
  }
  return null;
});

// Watchers
watch(containerName, (name) => {
  store.dispatch('page/setHeader', {
    title:       name || 'Container Info',
    description: '',
    action:      'ContainerStatusBadge',
  });
}, { immediate: true });

watch(activeTab, (tab) => {
  if (tab === 'tab-shell') {
    shellEverActivated.value = true;
    nextTick(() => containerShell.value?.focus());
  }
});

// Methods as functions
const onSearchInput = () => {
  containerLogs.value?.performSearch(searchTerm.value);
};

const searchNext = () => {
  containerLogs.value?.searchNext(searchTerm.value);
};

const searchPrevious = () => {
  containerLogs.value?.searchPrevious(searchTerm.value);
};

const clearSearch = () => {
  searchTerm.value = '';
  containerLogs.value?.clearSearch();
  nextTick(() => {
    searchInput.value?.focus();
  });
};

const handleSearchKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Enter') {
    if (event.shiftKey) {
      searchPrevious();
    } else {
      searchNext();
    }
    event.preventDefault();
  } else if (event.key === 'Escape') {
    clearSearch();
    event.preventDefault();
  }
};

const handleGlobalKeydown = (event: KeyboardEvent) => {
  if (event.key === '/') {
    // Don't trigger if search input is already focused
    if (!searchInput.value?.contains(document.activeElement)) {
      event.preventDefault();
      searchInput.value?.focus();
      searchInput.value?.select();
    }
  }
};

const clearError = () => {
  if (['containers', 'namespaces'].includes(error.value?.source || '')) {
    store.commit('container-engine/SET_ERROR', undefined);
  }
};

// Event handlers
// Lifecycle hooks
onMounted(() => {
  store.dispatch('container-engine/watchResources', ['containers']).catch(err =>
    store.commit('container-engine/SET_ERROR', { source: 'containers', error: err }));
  window.addEventListener('keydown', handleGlobalKeydown);
});

onBeforeUnmount(() => {
  store.dispatch('page/setHeader', { action: null });
  store.dispatch('container-engine/unwatchResources', ['containers']).catch(err =>
    console.error(err));

  window.removeEventListener('keydown', handleGlobalKeydown);
});
</script>

<style lang="scss" scoped>
.container-info-page {
  flex: 1;
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
  min-height: 0;
}

.search-widget {
  margin-left: auto;
  list-style: none;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.25rem 0.5rem;
  flex-shrink: 0;
}

.search-input {
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  background: var(--input-bg);
  color: var(--body-text);
  font-size: 13px;
  padding: 0 0.75rem;
  min-width: 200px;
  height: 32px;
  transition: border-color 0.2s ease;

  &::placeholder {
    color: var(--muted);
  }

  &:focus {
    border-color: var(--primary);
    outline: none;
  }
}

.search-btn {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 0;
  cursor: pointer;
  color: var(--body-text);
  transition: all 0.2s ease;
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 32px;
  min-height: 32px;

  &:hover:not(:disabled) {
    background: var(--primary);
    border-color: var(--primary);
    color: var(--primary-text);
  }

  &:disabled {
    opacity: 0.3;
    cursor: not-allowed;
  }

  &:focus-visible {
    outline: 2px solid var(--primary);
    outline-offset: -2px;
  }

  .icon {
    font-size: 12px;
  }
}

.tab-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

:deep(.container-logs-component),
:deep(.container-shell-component) {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
</style>
