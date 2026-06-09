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
      key-field="Name"
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
            <label>Namespace</label>
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
import { Banner } from '@rancher/components';
import merge from 'lodash/merge';
import { defineComponent } from 'vue';

import SortableTable from '@pkg/components/SortableTable';
import type { Settings } from '@pkg/config/settings';
import { mapTypedActions, mapTypedGetters, mapTypedMutations, mapTypedState } from '@pkg/entry/store';
import { hasField } from '@pkg/utils/iterator';
import { defined } from '@pkg/utils/typeUtils';
import { IoRancherdesktopContainersV1alpha1Volume as Volume } from '@rdd-client';

const MAX_PATH_LENGTH = 40;

/**
 * The RowItem type describes the type of one row.
 */
type RowItem = Volume & Required<Pick<Volume, 'status' | 'metadata'>> & {
  createdText:      string;
  availableActions: {
    label:       string;
    action:      string;
    enabled:     boolean;
    bulkable:    boolean;
    bulkAction?: string;
  }[];
  deleteVolume: (items?: RowItem[]) => void;
  browseFiles:  (items?: RowItem[]) => void;
};

export default defineComponent({
  name:       'Volumes',
  title:      'Volumes',
  components: { SortableTable, Banner },
  data() {
    return {
      settings:       undefined as Settings | undefined,
      headers:        [
        {
          name:  'Name',
          label: this.t('volumes.manage.table.header.volumeName'),
          sort:  ['Name'],
        },
        {
          name:  'Driver',
          label: this.t('volumes.manage.table.header.driver'),
          sort:  ['Driver', 'Name'],
        },
        {
          name:  'Mountpoint',
          label: this.t('volumes.manage.table.header.mountpoint'),
          sort:  ['Mountpoint', 'Name'],
        },
        {
          name:  'Created',
          label: this.t('volumes.manage.table.header.created'),
          sort:  ['Created', 'Name'],
          width: 120,
        },
      ],
    };
  },
  computed: {
    ...mapTypedState('container-engine', ['error', 'currentNamespace', 'volumes']),
    ...mapTypedState('container-engine', { namespaceObjects: 'namespaces' }),
    ...mapTypedGetters('container-engine', ['supportsNamespaces']),
    namespaces() {
      return (this.namespaceObjects ?? []).map(ns => ns.metadata?.name).filter(defined);
    },
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
              enabled:  true,
              bulkable: false,
            },
            {
              label:      this.t('volumes.manager.table.action.delete'),
              action:     'deleteVolume',
              enabled:    true,
              bulkable:   true,
              bulkAction: 'deleteVolume',
            },
          ],
          deleteVolume: (args?: Volume | Volume[]) => {
            const volumes = Array.isArray(args) ? args : [args].filter(defined);

            return Promise.all(volumes.map(volume => this.volumeDelete({ volume })));
          },
          browseFiles: () => {
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
    this.watchResources(['volumes']).catch(error => {
      this.SET_ERROR({ source: 'volumes', error });
    });
  },
  mounted() {
    this.setHeader({
      title:       this.t('volumes.title'),
      description: '',
    });
  },
  beforeUnmount() {
    this.unwatchResources(['volumes']);
  },
  methods: {
    ...mapTypedActions('container-engine', ['volumeDelete', 'watchResources', 'unwatchResources']),
    ...mapTypedActions('page', ['setHeader']),
    ...mapTypedMutations('container-engine', ['SET_ERROR', 'SET_CURRENT_NAMESPACE']),
    checkSelectedNamespace() {
      if (!this.supportsNamespaces || !this.namespaces.length) {
        return;
      }
      if (!this.namespaces.includes(this.currentNamespace ?? '')) {
        const K8S_NAMESPACE = 'k8s.io';
        const defaultNamespace = this.namespaces.includes(K8S_NAMESPACE) ? K8S_NAMESPACE : this.namespaces[0];
        this.SET_CURRENT_NAMESPACE(defaultNamespace);
      }
    },
    onChangeNamespace(event: Event) {
      const { value } = event.target as HTMLSelectElement;
      if (value !== this.currentNamespace) {
        this.SET_CURRENT_NAMESPACE(value);
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
