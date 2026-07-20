<template>
  <div class="general">
    <div>
      <ul>
        <li>Project Discussions: <b>#rancher-desktop</b> in <a href="https://slack.rancher.io/">Rancher Users</a> Slack</li>
        <li class="project-links">
          <span>Project Links:</span>
          <a href="https://github.com/rancher-sandbox/rancher-desktop">Homepage</a>
          <a href="https://github.com/rancher-sandbox/rancher-desktop/issues">Issues</a>
        </li>
      </ul>
    </div>
    <hr>
    <update-status
      preference="application.updates.enabled"
      :update-state="updateState"
      @apply="onUpdateApply"
    />
    <hr>
    <!--
    <telemetry-opt-in
      preference="application.telemetry.enabled"
    />
    <hr>
    -->
    <div class="network-status">
      <network-status />
    </div>
  </div>
</template>

<script lang="ts" setup>
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useStore } from 'vuex';

import packageJson from '@/package.json' with { type: 'json' };
import NetworkStatus from '@pkg/components/NetworkStatus.vue';
import UpdateStatus from '@pkg/components/UpdateStatus.vue';
import type { UpdateState } from '@pkg/main/update';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';

defineOptions({
  name:  'General',
  title: 'General',
});

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
    title:       store.getters['i18n/t']('general.title', { productName: packageJson.productName }),
    description: store.getters['i18n/t']('general.description'),
    icon:        'icon icon-rancher-desktop',
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
