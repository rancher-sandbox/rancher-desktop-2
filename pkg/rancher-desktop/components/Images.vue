<!--
  - This is the Images table in the K8s page.
  -->
<template>
  <div>
    <div
      v-if="ready"
      ref="fullWindow"
    >
      <SortableTable
        ref="imagesTable"
        class="imagesTable"
        data-test="imagesTableRows"
        key-field="_key"
        default-sort-by="name"
        :headers="headers"
        :rows="rows"
        no-rows-key="images.sortableTables.noRows"
        :table-actions="true"
        :paging="true"
        @selection="updateSelection"
      >
        <template #header-middle>
          <div class="header-middle">
            <Checkbox
              class="all-images"
              :value="showAll"
              :label="t('images.manager.table.label')"
              :disabled="!supportsShowAll"
              @update:value="handleShowAllCheckbox"
            />
            <div v-if="supportsNamespaces">
              <label>Namespace</label>
              <select
                class="select-namespace"
                :value="selectedNamespace"
                @change="handleChangeNamespace($event as any)"
              >
                <option
                  v-for="item in imageNamespaces"
                  :key="item"
                  :value="item"
                  :selected="item === selectedNamespace"
                >
                  {{ item }}
                </option>
              </select>
            </div>
          </div>
        </template>
        <template #col:id="{ row }:{ row: RowItem }">
          <td>
            <span v-tooltip="{ content: row.shortId ? row.id : undefined }">
              {{ row.shortId ?? row.id }}
            </span>
          </td>
        </template>
        <template #col:size="{ row }:{ row: RowItem }">
          <td>
            <span v-tooltip="{ content: row.size }">
              {{ sizeFormatter.format(row.size) }}
            </span>
          </td>
        </template>
        <!-- The SortableTable component puts the Filter box goes in the #header-right slot
             Too bad, because it means we can't use a css grid to manage the relative
             positions of these three widgets
        -->
      </SortableTable>

      <Card
        v-if="showImageManagerOutput"
        :show-highlight-border="false"
        :show-actions="false"
      >
        <template #title>
          <div class="type-title">
            <h3>{{ t('images.manager.title') }}</h3>
          </div>
        </template>
        <template #body>
          <images-output-window
            id="imageManagerOutput"
            ref="image-output-window"
            :current-command="currentCommand"
            :image-output-culler="imageOutputCuller"
            :show-status="false"
            :image-to-pull="imageToPull"
            @ok:process-end="resetCurrentCommand"
            @ok:show="toggleOutput"
          />
        </template>
      </Card>
    </div>
    <div v-else>
      <h3 v-if="!ready">
        {{ t('images.state.imagesUnready') }}
      </h3>
      <!-- TODO: actually handle this correctly -->
      <h3 v-else>
        {{ t('images.state.unknown') }}
      </h3>
    </div>
  </div>
</template>

<script lang="ts">
import { Card, Checkbox } from '@rancher/components';
import { defineComponent, PropType } from 'vue';

import ImagesOutputWindow from '@pkg/components/ImagesOutputWindow.vue';
import SortableTable from '@pkg/components/SortableTable';
import { mapTypedActions, mapTypedMutations, mapTypedState } from '@pkg/entry/store';
import getImageOutputCuller, { ImageOutputCuller } from '@pkg/utils/imageOutputCuller';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';
import { hasField } from '@pkg/utils/iterator';

import type * as RDDClient from '@rdd-client';
import type Electron from 'electron';

const untaggedTag = '<none>';

type Image = RDDClient.IoRancherdesktopContainersV1alpha1Image;
/**
 * ParsedImage represents an image with the `.status.repoTag` field parsed to
 * its component parts.
 */
type ParsedImage = Image & {
  /** The full repoTag reference; may be empty (for dangling images). */
  reference:  string;
  /** The name of the image, excluding the part after the `:`. */
  name:       string;
  /** The image registry; may be empty for `docker.io` images. */
  registry:   string;
  /** The image repository; may not contain a slash for `docker.io/library` images. */
  repository: string;
  /** The image tag, i.e. the part after the `:`. */
  tag:        string;
  /** The image ID, something like `sha256:abcdef...` */
  id:         string;
  /** Truncated image ID, for display purposes; e.g. `sha256:abc..def`. */
  shortId?:   string;
  /** The image size, in bytes. */
  size:       number;
  /** A sort key for the image. */
  _key:       string;
};
/**
 * RowItem represents a row in the table; this is ParsedImage plus actions.
 */
type RowItem = ParsedImage & {
  availableActions: {
    label:       string;
    action:      string;
    enabled:     boolean;
    icon:        string;
    bulkable?:   boolean;
    bulkAction?: string;
  }[];
  doPush:       () => void;
  deleteImage:  () => Promise<void>;
  deleteImages: () => Promise<void>;
  scanImage:    () => void;
};

export default defineComponent({
  components: {
    Card,
    Checkbox,
    SortableTable,
    ImagesOutputWindow,
  },
  props: {
    images: {
      type:     Array as PropType<Image[] | null>,
      required: true,
    },
    /**
     * List of images that should be protected; the list should only contain
     * the image name, excluding the tag; e.g.
     * `registry.opensuse.org/opensuse/leap` (excluding `:16.0`).
     */
    protectedImages: {
      type:    Array as PropType<string[]>,
      default: () => [],
    },
    imageNamespaces: {
      type:     Array as PropType<string[]>,
      required: true,
    },
    selectedNamespace: {
      type:    String,
      default: 'default',
    },
    supportsNamespaces: {
      type:    Boolean,
      default: false,
    },
    showAll: {
      type:    Boolean,
      default: false,
    },
  },

  data() {
    const sizeFormatter = Intl.NumberFormat(undefined, {
      style:       'unit',
      unit:        'byte',
      unitDisplay: 'narrow',
      notation:    'compact',
    });
    return {
      currentCommand: undefined as string | undefined,
      headers:
      [
        {
          name:  'name',
          label: this.t('images.manager.table.header.imageName'),
          sort:  ['name', 'tag', 'id'],
        },
        {
          name:  'tag',
          label: this.t('images.manager.table.header.tag'),
          sort:  ['tag', 'name', 'id'],
        },
        {
          name:  'id',
          label: this.t('images.manager.table.header.imageId'),
          sort:  ['id', 'name', 'tag'],
        },
        {
          name:  'size',
          label: this.t('images.manager.table.header.size'),
          sort:  ['size', 'name', 'tag', 'id'],
        },
      ],
      keepImageManagerOutputWindowOpen: false,
      imageOutputCuller:                undefined as ImageOutputCuller | undefined,
      mainWindowScroll:                 -1,
      selected:                         [] as RowItem[],
      imageToPull:                      undefined as string | undefined,
      sizeFormatter,
    };
  },
  computed: {
    ...mapTypedState('action-menu', { menuImages: state => state.resources?.map((i: RowItem) => i._key) ?? [] }),
    main() {
      return document.getElementsByTagName('main')[0];
    },
    parsedImages(): ParsedImage[] {
      // For our purposes, an image reference can be split up into:
      // registry.opensuse.org:443/opensuse/leap:15.6
      // ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^   reference
      // ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^        name
      // ^^^^^^^^^^^^^^^^^^^^^^^^^                      registry
      //                           ^^^^^^^^^^^^^        repository
      //                                         ^^^^   tag
      // If an image does not have a reference (i.e. untagged), we use the image ID as the name,
      // and '<none>' as the tag.
      return (this.images ?? []).filter(hasField('status')).map(image => {
        const reference = image.status.repoTag ?? '';
        const [, name, tag] = /^(.*):([^/:]*)$/.exec(reference) ?? ['', reference || image.status.id, untaggedTag];
        // The registry part must have at least one dot or colon (for port), because it's a host name.
        const [, registry, repository] = /^([^/]+[.:][^/]+)\/(.*)$/.exec(name) ?? ['', '', name];

        return {
          ...image,
          reference,
          name,
          registry,
          repository,
          tag,
          id:      image.status.id,
          shortId: this.shortHash(image.status.id),
          size:    image.status.size,
          _key:    `${ image.status.id }-${ reference }-${ image.metadata?.name }`,
        };
      });
    },
    filteredImages() {
      // Images with '<none>' or empty name are not allowed at the moment.
      const filteredImages = this.parsedImages.filter(image => image.reference);

      if (!this.supportsShowAll || this.showAll) {
        return filteredImages;
      }

      return filteredImages.filter(this.isDeletable);
    },
    ready() {
      return Array.isArray(this.images);
    },
    imagesToDelete(): RowItem[] {
      return this.selected.filter(image => this.isDeletable(image));
    },
    imageIdsToDelete() {
      return this.imagesToDelete.map(image => image.reference || image.id);
    },
    rows(): RowItem[] {
      return this.filteredImages
        .map(image => ({
          ...image,
          // The `availableActions` property is used by the ActionMenu to fill
          // out the menu entries.
          availableActions: [
            {
              label:   this.t('images.manager.table.action.push'),
              action:  'doPush',
              enabled: this.isPushable(image),
              icon:    'icon icon-upload',
            },
            {
              label:      this.t('images.manager.table.action.delete'),
              action:     'deleteImage',
              enabled:    this.isDeletable(image),
              icon:       'icon icon-delete',
              bulkable:   true,
              bulkAction: 'deleteImages',
            },
            {
              label:   this.t('images.manager.table.action.scan'),
              action:  'scanImage',
              enabled: true,
              icon:    'icon icon-info-circle',
            },
          ].filter(x => x.enabled),
          // ActionMenu callbacks - SortableTable assumes that these methods live
          // on the rows directly.
          doPush:       this.doPush.bind(this, image),
          deleteImage:  this.deleteImage.bind(this, image),
          deleteImages: this.deleteImages.bind(this),
          scanImage:    this.scanImage.bind(this, image),
        }));
    },
    showImageManagerOutput() {
      return this.keepImageManagerOutputWindowOpen;
    },
    supportsShowAll() {
      return this.selectedNamespace === 'k8s.io';
    },
  },

  watch: {
    rows: {
      // Hide the action menu if some of the images that the menu would act upon
      // have been updated while the menu was open.
      handler(newRows: RowItem[]) {
        if (this.menuImages.some(name => newRows.map(r => r._key).includes(name))) {
          this.hideMenu();
        }
      },
      deep: true,
    },
  },

  methods: {
    ...mapTypedActions('container-engine', ['imageDelete', 'imagePush']),
    ...mapTypedMutations('action-menu', { hideMenu: 'hide' }),
    updateSelection(val: RowItem[]) {
      this.selected = val;
    },
    startImageManagerOutput() {
      this.keepImageManagerOutputWindowOpen = true;
      this.scrollToOutputWindow();
    },
    scrollToOutputWindow() {
      this.$nextTick(() => {
        if (this.main) {
          // move to the bottom
          this.main.scrollTop = this.main.scrollHeight;
        }
      });
    },
    scrollToTop() {
      this.$nextTick(() => {
        try {
          if (this.main) {
            this.main.scrollTop = this.mainWindowScroll;
          }
        } catch (e) {
          console.log(`Trying to reset scroll to ${ this.mainWindowScroll }, got error:`, e);
        }

        this.mainWindowScroll = -1;
      });
    },
    startRunningCommand(command: Parameters<typeof getImageOutputCuller>[0]) {
      this.imageOutputCuller = getImageOutputCuller(command);
    },
    async deleteImages() {
      const message = `Delete ${ this.imagesToDelete.length } ${ this.imagesToDelete.length > 1 ? 'images' : 'image' }?`;
      const detail = this.imageIdsToDelete.join('\n');

      const options: Electron.MessageBoxOptions = {
        message,
        detail,
        type:      'question',
        buttons:   ['Yes', 'No'],
        defaultId: 1,
        title:     'Confirming image deletion',
        cancelId:  1,
      };

      const result = await ipcRenderer.invoke('show-message-box', options);

      if (result.response === 1) {
        return;
      }

      // TODO: This should display deletion output.
      await Promise.all(this.imagesToDelete.map(image => this.imageDelete({ image })));
    },
    async deleteImage(image: ParsedImage) {
      const options: Electron.MessageBoxOptions = {
        message:   `Delete image ${ image.name }:${ image.tag }?`,
        type:      'question',
        buttons:   ['Yes', 'No'],
        defaultId: 1,
        title:     'Confirming image deletion',
        cancelId:  1,
      };
      const result = await ipcRenderer.invoke('show-message-box', options);

      if (result.response === 1) {
        return;
      }

      // TODO: This should display deletion output.
      await this.imageDelete({ image });
    },
    doPush(image: ParsedImage) {
      // TODO: This should display push output.
      this.imagePush({ image });
    },
    scanImage(row: ParsedImage) {
      this.$router.push({
        name:   'images-scans-image-name',
        params: { image: row.reference || row.id, namespace: this.selectedNamespace },
      });
    },
    isDeletable(row: ParsedImage) {
      return !this.protectedImages.includes(row.name);
    },
    isPushable(row: ParsedImage) {
      // If it doesn't contain a '/', it's certainly not pushable,
      // but having a '/' isn't sufficient, but it's all we have to go on.
      return this.isDeletable(row) && row.name.includes('/');
    },
    handleShowAllCheckbox(value: boolean) {
      this.$emit('toggledShowAll', value);
    },
    handleChangeNamespace(event: Event & { target: HTMLSelectElement }) {
      this.$emit('switchNamespace', event.target.value);
    },
    resetCurrentCommand() {
      this.currentCommand = undefined;
    },
    toggleOutput(val: boolean) {
      this.keepImageManagerOutputWindowOpen = val;

      if (!val && this.mainWindowScroll >= 0) {
        this.scrollToTop();
      }
    },
    shortHash(sha: string) {
      const length = 6;
      const [, prefix, actualHash] = new RegExp(`^([^:]+:)(.{${ length * 2 },})$`).exec(sha) ?? [];

      if (!prefix) {
        return undefined;
      }

      return `${ prefix }${ actualHash.slice(0, length) }..${ actualHash.slice(-length) }`;
    },
  },
});
</script>

<style lang="scss" scoped>
  .labeled-input > .btn {
    position: absolute;
    bottom: -1px;
    right: -1px;
    border-start-start-radius: var(--border-radius);
    border-radius: var(--border-radius) 0 0 0;
  }

  @keyframes highlightFade {
    from {
      background: var(--accent-btn);
    } to {
      background: transparent;
    }
  }

  .select-namespace {
    max-width: 24rem;
    min-width: 8rem;
  }

  .header-middle {
    display: flex;
    align-items: flex-end;
    gap: 1rem;
    height: 100%;
  }

  .all-images {
    margin-bottom: 12px;
  }

  .imagesTable :deep(.search-box) {
    align-self: flex-end;
  }
  .imagesTable :deep(.bulk) {
    align-self: flex-end;
  }
</style>
