<script setup lang="ts">
import { onMounted, provide, readonly, ref } from 'vue';
import { useStore } from 'vuex';

import { binDirInjectionKey } from '@pkg/types/injectionKeys';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';

const store = useStore();
const binDir = ref('~/.rd2/bin');

ipcRenderer.invoke('get-paths').then(result => {
  binDir.value = result.bin_dir;
}).catch(e => {
  console.error('Failed to get paths:', e);
});

provide(binDirInjectionKey, readonly(binDir));

defineOptions({ name: 'preferences-layout' });

onMounted(() => {
  store.dispatch('i18n/init').catch(ex => console.error(ex));
  ipcRenderer.send('preferences/load');
});
</script>

<template>
  <div class="wrapper">
    <RouterView />
  </div>
</template>

<style lang="scss" src="@pkg/assets/styles/app.scss"></style>
<style lang="scss" scoped>
.wrapper {
  background-color: var(--body-bg);
}
</style>
