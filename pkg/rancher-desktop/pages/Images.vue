<template>
  <div>
    <RouterView />
    <Images
      class="content"
      data-test="imagesTable"
      :images="images"
      :image-namespaces="namespaces"
      :show-all="settings.images.showAll"
      :selected-namespace="currentNamespace"
      :supports-namespaces="supportsNamespaces"
      :protected-images="protectedImages"
      @toggled-show-all="onShowAllImagesChanged"
      @switch-namespace="onChangeNamespace"
    />
  </div>
</template>

<script lang="ts">
import { defineComponent } from 'vue';

import Images from '@pkg/components/Images.vue';
import { defaultSettings } from '@pkg/config/settings';
import { mapTypedActions, mapTypedGetters, mapTypedMutations, mapTypedState } from '@pkg/entry/store';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';
import { defined } from '@pkg/utils/typeUtils';

export default defineComponent({
  components: { Images },
  data() {
    return {
      settings: defaultSettings,
    };
  },

  computed: {
    ...mapTypedGetters('extensions', ['installedExtensions']),
    ...mapTypedGetters('container-engine', ['supportsNamespaces']),
    ...mapTypedState('rdd-connection', { kubeNamespace: 'namespace' }),
    ...mapTypedState('container-engine', ['currentNamespace', 'images']),
    ...mapTypedState('container-engine', { namespaceObjects: 'namespaces' }),
    namespaces() {
      return (this.namespaceObjects ?? []).map(ns => ns.metadata?.name).filter(defined);
    },
    rancherImages(): string[] {
      return (this.images ?? [])
        .map(image => image.status?.repoTag)
        .filter(defined)
        .map(reference => reference.replace(/:[^/]*?$/, ''))
        .filter(name => name.startsWith('rancher/'));
    },
    installedExtensionImages(): string[] {
      return this.installedExtensions.map(image => image.id);
    },
    protectedImages(): string[] {
      // This should be replaced with something on the image; see
      // https://github.com/rancher-sandbox/rancher-desktop-daemon/issues/193
      return [
        'moby/buildkit',
        'ghcr.io/rancher-sandbox/rancher-desktop/rdx-proxy',
        ...this.rancherImages,
        ...this.installedExtensionImages,
      ];
    },
  },

  watch: {
    images: {
      handler(images) {
        if (Array.isArray(images)) {
          this.setAction({ action: 'ImagesButtonAdd' });
        }
      },
      immediate: true,
    },
  },

  beforeMount() {
    this.setHeader({ title: this.t('images.title') });
    this.watchResources(['namespaces', 'images']).catch(error => {
      this.SET_ERROR({ source: 'images', error });
    });
  },

  mounted() {
    // TODO: Handle setting change (for namespaces).
    ipcRenderer.on('extensions/changed', this.fetchExtensions);
    this.fetchExtensions();
  },
  beforeUnmount() {
    this.unwatchResources(['namespaces', 'images']);
    ipcRenderer.removeListener('extensions/changed', this.fetchExtensions);
  },

  methods: {
    ...mapTypedActions('container-engine', ['setCurrentNamespace', 'watchResources', 'unwatchResources']),
    ...mapTypedMutations('container-engine', ['SET_ERROR']),
    ...mapTypedActions('extensions', { fetchExtensions: 'fetch' }),
    ...mapTypedActions('page', ['setAction', 'setHeader']),
    onShowAllImagesChanged(value: boolean) {
      // TODO: This should update the namespace.
      console.log('onShowAllImagesChanged', value);
    },
    async onChangeNamespace(namespace: string) {
      await this.setCurrentNamespace({ namespace });
    },
  },
});
</script>
