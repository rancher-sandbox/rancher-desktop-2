<template>
  <div class="general">
    <div>
      <ul>
        <!-- v-clean-html: the translated string embeds a link -->
        <li v-clean-html="t('general.projectDiscussions')" />
        <li class="project-links">
          <span>{{ t('general.projectLinks') }}</span>
          <a href="https://github.com/rancher-sandbox/rancher-desktop-2">{{ t('general.homepage') }}</a>
          <a href="https://github.com/rancher-sandbox/rancher-desktop-2/issues">{{ t('general.issues') }}</a>
        </li>
      </ul>
    </div>
    <hr>
    <update-status
      preference="application.updates.enabled"
      :update-state="updateState"
      @apply="onUpdateApply"
    />
    <blog-feed />
  </div>
</template>

<script lang="ts" setup>
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useStore } from 'vuex';

import BlogFeed from '@pkg/components/BlogFeed.vue';
import UpdateStatus from '@pkg/components/UpdateStatus.vue';
import type { UpdateState } from '@pkg/main/update';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';

defineOptions({ name: 'General' });

const store = useStore();
const updateState = ref<UpdateState | null>(null);

function onUpdateApply() {
  ipcRenderer.send('update-apply');
}

function onUpdateState(_event: Electron.IpcRendererEvent, state: UpdateState) {
  updateState.value = state;
}

onMounted(() => {
  store.dispatch('page/setHeader', {
    titleKey:       'general.title',
    descriptionKey: 'general.description',
    icon:           'icon icon-rancher-desktop',
    action:         'AutoUpdateCheckbox',
  });
  ipcRenderer.on('update-state', onUpdateState);
  ipcRenderer.send('update-state');
});

onBeforeUnmount(() => {
  ipcRenderer.removeListener('update-state', onUpdateState);
});
</script>

<!-- Add "scoped" attribute to limit CSS to this component only -->
<style scoped lang="scss">
.general {
  display: flex;
  flex-direction: column;
  gap: 0.625rem;
  // Fill the body so the blog feed can claim whatever height is left over.
  flex: 1;
  min-height: 0;

  ul {
    margin-bottom: 0;

    li {
      margin-bottom: .5em;
    }
  }
}

.project-links > * {
  margin-right: .25em;
}
</style>
