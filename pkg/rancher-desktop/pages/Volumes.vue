<template>
  <div class="volumes">
    <banner
      v-if="errorMessage"
      color="error"
      data-testid="error-banner"
      @close="clearError"
    >
      {{ errorMessage }}
    </banner>
    <SortableTable
      class="volumesTable"
      data-testid="volumes-table"
      :headers="headers"
      key-field="status.name"
      :rows="rows"
      no-rows-key="volumes.sortableTables.noRows"
      :row-actions="true"
      :paging="true"
      :rows-per-page="10"
      :has-advanced-filtering="false"
      :loading="!volumes"
    >
      <template #header-middle>
        <div class="header-middle">
          <div v-if="supportsNamespaces">
            <label>{{ t('generic.namespace') }}</label>
            <select
              :value="currentNamespace"
              class="select-namespace"
              data-testid="namespace-selector"
              @change="onChangeNamespace($event)"
            >
              <option
                v-for="item in namespaces ?? []"
                :key="item"
                :selected="item === currentNamespace"
                :value="item"
              >
                {{ item }}
              </option>
            </select>
          </div>
        </div>
      </template>
      <template #col:Name="{ row } : { row: RowItem }">
        <td data-testid="volume-name-cell">
          <span v-tooltip="getTooltipConfig(row.status.name)">
            {{ shortHash(row.status.name) }}
          </span>
        </td>
      </template>
      <template #col:Driver="{ row } : { row: RowItem }">
        <td data-testid="volume-driver-cell">
          {{ row.status.driver }}
        </td>
      </template>
      <template #col:Mountpoint="{ row } : { row: RowItem }">
        <td data-testid="volume-mountpoint-cell">
          <span v-tooltip="getTooltipConfig(row.status.mountpoint)">
            {{ shortPath(row.status.mountpoint) }}
          </span>
        </td>
      </template>
      <template #col:Created="{ row } : { row: RowItem }">
        <td data-testid="volume-created-cell">
          {{ row.createdText }} <!-- use the text representation -->
        </td>
      </template>
    </SortableTable>
  </div>
</template>

<script lang="ts">
import merge from 'lodash/merge';
import { defineComponent } from 'vue';

import Banner from '@pkg/components/Banner.vue';
import SortableTable from '@pkg/components/SortableTable';
import type { Settings } from '@pkg/config/settings';
import { mapTypedActions, mapTypedGetters, mapTypedMutations, mapTypedState } from '@pkg/entry/store';
import { hasField } from '@pkg/utils/iterator';
import { IoRancherdesktopContainersV1alpha1Volume as Volume } from '@rdd-client';

const MAX_PATH_LENGTH = 40;

/**
 * The RowItem type describes the type of one row.
 */
type RowItem = Volume & Required<Pick<Volume, 'status' | 'metadata'>> & {
  createdText:      string;
  availableActions: {
    label:    string;
    action:   string;
    enabled:  boolean;
    bulkable: boolean;
  }[];
  deleteVolume: (this: RowItem) => void;
  browseFiles:  (this: RowItem) => void;
};

export default defineComponent({
  name:       'Volumes',
  components: { SortableTable, Banner },
  data() {
    return {
      settings:       undefined as Settings | undefined,
      headers:        [
        {
          name:  'Name',
          label: this.t('volumes.manage.table.header.volumeName'),
          sort:  ['status.name'],
        },
        {
          name:  'Driver',
          label: this.t('volumes.manage.table.header.driver'),
          sort:  ['status.driver', 'status.name'],
        },
        {
          name:  'Mountpoint',
          label: this.t('volumes.manage.table.header.mountpoint'),
          sort:  ['status.mountpoint', 'status.name'],
        },
        {
          name:  'Created',
          label: this.t('volumes.manage.table.header.created'),
          sort:  ['status.createdAt', 'status.name'],
          width: 120,
        },
      ],
    };
  },
  computed: {
    ...mapTypedState('container-engine', ['error', 'volumes']),
    ...mapTypedGetters('container-engine', ['supportsNamespaces', 'namespaces', 'currentNamespace']),
    rows(): RowItem[] {
      return (this.volumes ?? [])
        .filter(hasField('metadata'))
        .filter(hasField('status'))
        .sort((a, b) => a.status.name.localeCompare(b.status.name))
        .map(volume => merge({}, volume, {
          createdText:          volume.status.createdAt ? new Date(volume.status.createdAt).toLocaleDateString() : '',
          availableActions: [
            {
              label:    this.t('volumes.manager.table.action.browse'),
              action:   'browseFiles',
              enabled:  false,
              bulkable: false,
            },
            {
              label:     this.t('volumes.manager.table.action.delete'),
              action:    'deleteVolume',
              enabled:   true,
              bulkable:  true,
            },
          ],
          deleteVolume: () => this.volumeDelete({ volume }),
          browseFiles:  () => {
            this.$router.push({ name: 'volumes-files-name', params: { name: volume.status.name } });
          },
        }));
    },
    errorMessage() {
      switch (this.error?.source) {
      case 'namespaces': case 'volumes': {
        const error: any = this.error.error;

        return `${ error?.stderr ?? error }`;
      }
      }
      return null;
    },
  },
  beforeMount() {
    this.watchResources(['namespaces', 'volumes']).catch(error => {
      this.SET_ERROR({ source: 'volumes', error });
    });
  },
  mounted() {
    this.setHeader({ titleKey: 'volumes.title' });
  },
  beforeUnmount() {
    this.unwatchResources(['namespaces', 'volumes']);
  },
  methods: {
    ...mapTypedActions('container-engine', ['volumeDelete', 'watchResources', 'unwatchResources', 'setCurrentNamespace']),
    ...mapTypedActions('page', ['setHeader']),
    ...mapTypedMutations('container-engine', ['SET_ERROR']),
    onChangeNamespace(event: Event) {
      const { value } = event.target as HTMLSelectElement;
      if (value !== this.currentNamespace) {
        this.setCurrentNamespace({ namespace: value });
      }
    },
    shortHash(hash: string) {
      const [_, prefix, actualHash] = /^([^:]+:)(.+)$/.exec(hash) ?? [];

      if (!prefix) {
        return hash;
      }

      return `${ prefix }${ actualHash.slice(0, 3) }..${ actualHash.slice(-3) }`;
    },
    shortPath(path: string) {
      if (!path || path.length <= MAX_PATH_LENGTH) {
        return path || '';
      }

      return `${ path.slice(0, 20) }...${ path.slice(-17) }`;
    },
    getTooltipConfig(text: string) {
      if (!text) {
        return { content: undefined };
      }

      // Show tooltip for sha256 hashes or long paths
      if (text.startsWith('sha256:') || text.length > MAX_PATH_LENGTH) {
        return { content: text };
      }

      return { content: undefined };
    },
    clearError() {
      switch (this.error?.source) {
      case 'namespaces': case 'volumes':
        this.SET_ERROR(undefined);
      }
    },
  },
});
</script>

<style lang="scss" scoped>
.volumes {
  &-status {
    padding: 8px 5px;
  }
}

.select-namespace {
  max-width: 24rem;
  min-width: 8rem;
}

.volumesTable:v-deep(.search-box) {
  align-self: flex-end;
}
.volumesTable:v-deep(.bulk) {
  align-self: flex-end;
}
</style>
