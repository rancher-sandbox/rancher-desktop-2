<template>
  <div class="containers">
    <banner
      v-if="errorMessage"
      color="error"
      @close="clearError"
    >
      {{ errorMessage }}
    </banner>
    <SortableTable
      ref="sortableTableRef"
      class="containersTable"
      :headers="headers"
      key-field="id"
      :rows="rows"
      no-rows-key="containers.sortableTables.noRows"
      :row-actions="true"
      :paging="true"
      :rows-per-page="10"
      :has-advanced-filtering="false"
      :loading="containers === null"
      group-by="projectGroup"
      :group-sort="['projectGroup']"
    >
      <template #header-middle>
        <div class="header-middle">
          <div v-if="supportsNamespaces">
            <label>{{ t('generic.namespace') }}</label>
            <select
              class="select-namespace"
              :value="currentNamespace"
              @change="onChangeNamespace($event)"
            >
              <option
                v-for="item in namespaces"
                :key="item"
                :value="item"
                :selected="item === currentNamespace"
              >
                {{ item }}
              </option>
            </select>
          </div>
        </div>
      </template>
      <template #col:containerState="{ row }: { row: RowItem }">
        <td>
          <container-status-badge :container="row" />
        </td>
      </template>
      <template #col:imageName="{ row }: { row: RowItem }">
        <td>
          <span v-tooltip="getTooltipConfig(row.status?.image || 'unknown')">
            {{ shortSha(row.imageName || 'unknown') }}
          </span>
        </td>
      </template>
      <template #col:containerName="{ row }: { row: RowItem }">
        <td>
          <a
            v-tooltip="getTooltipConfig(row.status?.name || row.metadata?.name || 'unknown')"
            class="container-name-link"
            @click.stop.prevent="viewInfo(row)"
          >
            {{ shortSha(row.status?.name || row.metadata?.name || 'unknown') }}
          </a>
        </td>
      </template>
      <template #col:ports="{ row }">
        <td>
          <div class="port-container">
            <a
              v-for="[hostPort, containerPort] in row.portList.slice(0, 2)"
              :key="hostPort"
              target="_blank"
              class="link"
              @click="openUrl(hostPort)"
            >
              {{ hostPort }}:{{ containerPort }}
            </a>

            <div
              v-if="shouldHaveDropdown(row.portList)"
              class="dropdown"
              @mouseenter="addDropDownPosition"
              @mouseleave="clearDropDownPosition"
            >
              <span>
                ...
              </span>
              <div class="dropdown-content">
                <a
                  v-for="[hostPort, containerPort] in row.portList.slice(2)"
                  :key="hostPort"
                  target="_blank"
                  class="link"
                  @click="openUrl(hostPort)"
                >
                  {{ hostPort }}:{{ containerPort }}
                </a>
              </div>
            </div>
          </div>
        </td>
      </template>
      <template #group-row="{ group }">
        <tr
          class="group-row"
          :aria-expanded="!collapsed[group.ref]"
        >
          <td :colspan="headers.length + 1">
            <div class="group-tab">
              <i
                data-title="Toggle Expand"
                :class="{
                  icon: true,
                  'icon-chevron-right': !!collapsed[group.ref],
                  'icon-chevron-down': !collapsed[group.ref],
                }"
                @click.stop="toggleExpand(group.ref)"
              />
              {{ groupLabel(group.ref) }}
              <span v-if="!!collapsed[group.ref]"> ({{ group.rows.length }})</span>
            </div>
          </td>
        </tr>
      </template>
    </SortableTable>
  </div>
</template>

<script lang="ts">
import dayjs from 'dayjs';
import { shell } from 'electron';
import { defineComponent } from 'vue';

import Banner from '@pkg/components/Banner.vue';
import ContainerStatusBadge from '@pkg/components/ContainerStatusBadge.vue';
import SortableTable from '@pkg/components/SortableTable';
import { mapTypedActions, mapTypedGetters, mapTypedMutations, mapTypedState } from '@pkg/entry/store';
import { showDeleteConfirmation } from '@pkg/utils/deleteConfirmation';
import { hasField } from '@pkg/utils/iterator';
import { defined } from '@pkg/utils/typeUtils';
import { IoRancherdesktopContainersV1alpha1Container as Container } from '@rdd-client';

interface Action {
  label:       string;
  action:      string;
  enabled:     boolean;
  bulkable:    boolean;
  bulkAction?: string;
}

type RowItem = Container & {
  uptime:            string;
  id:                string;
  imageName:         string | undefined;
  portsSortKey:      number[];
  availableActions?: Action[];
  startContainer:    (this: Container) => void;
  stopContainer:     (this: Container) => void;
  pauseContainer:    (this: Container) => void;
  unpauseContainer:  (this: Container) => void;
  restartContainer:  (this: Container) => void;
  deleteContainer:   (containers?: Container[]) => Promise<void>;
  viewInfo:          (this: Container) => void;
  portList:          (readonly [number, number])[];
};

/**
 * The projectGroup of containers outside any compose project or Kubernetes pod.
 * It doubles as the grouping and sort key, so it stays untranslated;
 * groupLabel() shows a translated label in its place.
 */
const STANDALONE_CONTAINERS_GROUP = 'Standalone Containers';

export default defineComponent({
  name:       'Containers',
  components: { SortableTable, ContainerStatusBadge, Banner },
  data() {
    return {
      // The type cast is necessary to correctly type `collapsed`.
      // eslint-disable-next-line @typescript-eslint/no-unnecessary-type-assertion
      collapsed: {} as Record<string, boolean>,
      headers:   [
        {
          name:  'containerState',
          label: this.t('containers.manage.table.header.state'),
        },
        {
          name:  'containerName',
          label: this.t('containers.manage.table.header.containerName'),
          sort:  ['status.name', 'imageName'],
        },
        {
          name:  'imageName',
          label: this.t('containers.manage.table.header.image'),
          sort:  ['imageName', 'status.name'],
        },
        {
          name:  'ports',
          label: this.t('containers.manage.table.header.ports'),
          sort:  ['portsSortKey', 'status.name', 'imageName'],
        },
        {
          name:  'uptime',
          label: this.t('containers.manage.table.header.started'),
          sort:  ['status.startedAt', 'status.name', 'imageName'],
          width: 120,
        },
      ],
    };
  },
  computed: {
    ...mapTypedState('container-engine', ['containers', 'images', 'currentNamespace', 'error']),
    ...mapTypedGetters('container-engine', ['supportsNamespaces', 'namespaces']),
    rows(): RowItem[] {
      const StatusRunning = 'running';
      return (this.containers ?? [])
        .filter(hasField('metadata'))
        .filter(hasField('status'))
        .filter(container => {
          // Filter out containers from the 'kube-system' namespace
          return this.supportsNamespaces || container.status.labels?.['io.kubernetes.pod.namespace'] !== 'kube-system';
        })
        .sort((a, b) => {
          // Sort by status, showing running first.
          if ((a.status.status === StatusRunning || b.status.status === StatusRunning) && a.status.status !== b.status.status) {
            // One of the two is running; put that first.
            return a.status.status === StatusRunning ? -1 : 1;
          }
          // Both or running, or neither.
          return a.status.status.localeCompare(b.status.status) || a.metadata.name?.localeCompare(b.metadata.name ?? '') || 0;
        })
        .map<RowItem>(container => {
          const portList = this.getPortList(container);
          return {
            ...container,
            uptime:           container.status.startedAt ? dayjs(container.status.startedAt).toNow(true) : '',
            id:               container.metadata.name!,
            imageName: (() => {
              const image = this.images?.find(image => image.status?.id === container.status?.image);
              return image?.status?.repoTag ?? container.status?.image;
            })(),
            projectGroup:     (() => {
              const labels = container.status.labels ?? {};
              const k8sPodName = labels['io.kubernetes.pod.name'];
              const k8sNamespace = labels['io.kubernetes.pod.namespace'];
              const composeProject = labels['com.docker.compose.project'];
              if (k8sPodName && k8sNamespace) {
                return `${ k8sNamespace }/${ k8sPodName }`;
              } else if (composeProject) {
                return composeProject;
              }
              return STANDALONE_CONTAINERS_GROUP;
            })(),
            availableActions: this.getContainerActions(container),
            startContainer:   () => this.containerRequestAction({ container, state: 'start' }),
            stopContainer:    () => this.containerRequestAction({ container, state: 'stop' }),
            pauseContainer:   () => this.containerRequestAction({ container, state: 'pause' }),
            unpauseContainer: () => this.containerRequestAction({ container, state: 'unpause' }),
            restartContainer: () => this.containerRequestAction({ container, state: 'restart' }),
            deleteContainer:  async(args?: Container[]) => {
              const targets = args?.length ? args : [container];

              if (await this.confirmDelete(targets)) {
                await Promise.all(targets.map(target => this.containerDelete({ container: target })));
              }
            },
            viewInfo:         () => this.viewInfo(container),
            portList,
            portsSortKey:     portList.map(([hostPort]) => hostPort).sort((a, b) => a - b),
          };
        });
    },
    errorMessage(): string | null {
      if (['containers', 'images', 'namespaces'].includes(this.error?.source ?? '')) {
        const error = this.error?.error;
        return String(error?.message ?? error ?? this.error);
      }
      return null;
    },
  },
  beforeMount() {
    this.watchResources(['containers', 'images', 'namespaces']).catch(error => {
      this.SET_ERROR({ source: 'containers', error });
    });
  },
  mounted() {
    this.setHeader({
      titleKey: 'containers.title',
    });
  },
  beforeUnmount() {
    this.unwatchResources(['containers', 'images', 'namespaces']);
  },
  methods: {
    ...mapTypedActions('page', ['setHeader']),
    ...mapTypedActions('container-engine', ['containerDelete', 'containerRequestAction', 'setCurrentNamespace', 'watchResources', 'unwatchResources']),
    ...mapTypedMutations('container-engine', ['SET_ERROR']),
    onChangeNamespace(event: Event) {
      const { value } = event.target as HTMLSelectElement;
      this.setCurrentNamespace({ namespace: value });
    },
    /**
     * Ask the user to confirm deleting the given containers.
     */
    async confirmDelete(containers: Container[]): Promise<boolean> {
      return await showDeleteConfirmation({
        title:   this.t('containers.confirmDelete.title'),
        message: this.t('containers.confirmDelete.message', { count: containers.length }),
        confirm: this.t('containers.confirmDelete.confirm'),
        cancel:  this.t('generic.cancel'),
        names:   containers.map(container => container.status?.name || container.metadata?.name || ''),
      });
    },
    getContainerActions(container: Container) {
      return [
        {
          label:      this.t('containers.manage.table.action.info'),
          action:     'viewInfo',
          enabled:    true,
          bulkable:   false,
        },
        {
          label:      this.t('containers.manage.table.action.start'),
          action:     'startContainer',
          enabled:    this.isStopped(container),
          bulkable:   true,
        },
        {
          label:      this.t('containers.manage.table.action.stop'),
          action:     'stopContainer',
          enabled:    this.isRunning(container) || this.isPaused(container),
          bulkable:   true,
        },
        {
          label:      'Pause',
          action:     'pauseContainer',
          enabled:    this.isRunning(container),
          bulkable:   true,
        },
        {
          label:      'Unpause',
          action:     'unpauseContainer',
          enabled:    this.isPaused(container),
          bulkable:   true,
        },
        {
          label:      this.t('containers.manage.table.action.restart'),
          action:     'restartContainer',
          enabled:    this.isRunning(container),
          bulkable:   true,
        },
        {
          label:      this.t('images.manager.table.action.delete'),
          action:     'deleteContainer',
          enabled:    this.isStopped(container),
          bulkable:   true,
          bulkAction: 'deleteContainer',
        },
      ];
    },
    clearDropDownPosition(event: Event) {
      const target = event.target as HTMLElement;
      const dropdownContent = target.querySelector<HTMLElement>('.dropdown-content');

      if (dropdownContent) {
        dropdownContent.style.top = '';
      }
    },
    addDropDownPosition(event: Event) {
      const tableRef: any = this.$refs.sortableTableRef;
      const table = tableRef.$el;
      const target = event.target as HTMLElement;
      const dropdownContent = target.querySelector<HTMLElement>('.dropdown-content');

      if (dropdownContent) {
        const dropdownRect = target.getBoundingClientRect();
        const tableRect = table.getBoundingClientRect();
        const targetTopPos = dropdownRect.top - tableRect.top;
        const tableHeight = tableRect.height;

        if (targetTopPos < tableHeight / 2) {
          // Show dropdownContent below the target
          dropdownContent.style.top = `${ dropdownRect.bottom }px`;
        } else {
          // Show dropdownContent above the target
          dropdownContent.style.top = `${ dropdownRect.top - dropdownContent.getBoundingClientRect().height }px`;
        }
      }
    },
    viewInfo(container: Container) {
      this.$router.push(`/containers/info/${ container.metadata!.name }`);
    },
    isRunning(container: Container) {
      return container.status?.status === 'running';
    },
    isStopped(container: Container) {
      return ['created', 'exited'].includes(container.status?.status ?? 'unknown');
    },
    isPaused(container: Container) {
      return container.status?.status === 'paused';
    },
    shortSha(sha: string) {
      const prefix = 'sha256:';

      if (sha.includes(prefix)) {
        const startIndex = sha.indexOf(prefix) + prefix.length;
        const actualSha = sha.slice(startIndex);

        return `${ sha.slice(0, startIndex) }${ actualSha.slice(0, 3) }..${ actualSha.slice(-3) }`;
      }

      return sha;
    },
    getTooltipConfig(sha: string) {
      if (!sha.includes('sha256:')) {
        return { content: undefined };
      }

      return { content: sha };
    },
    /**
     * @returns {[number, number][]} (host port, container port) tuples, sorted by host port.
     */
    getPortList(container: Container): (readonly [number, number])[] {
      return (container.status?.ports ?? []).flatMap(({ name, bindings }) => {
        const containerPort = parseInt(name.split('/')[0], 10);
        return (bindings ?? []).map(binding => {
          return [parseInt(binding.hostPort, 10), containerPort] as const;
        });
      }).reduce((acc, [hostPort, containerPort]) => {
        // Avoid duplicates
        if (hostPort && !acc.some(([existingHostPort]) => existingHostPort === hostPort)) {
          acc.push([hostPort, containerPort]);
        }
        return acc;
      }, [] as (readonly [number, number])[]).sort(([hostPortA], [hostPortB]) => {
        return hostPortA - hostPortB;
      });
    },
    shouldHaveDropdown(ports: (readonly [number, number])[]): boolean {
      if (!ports) {
        return false;
      }

      return ports.length >= 3;
    },
    openUrl(hostPort: number) {
      const url = {
        80:  'http://localhost',
        443: 'https://localhost',
      }[hostPort] ?? `http://localhost:${ hostPort }`;

      shell.openExternal(url);
    },

    toggleExpand(group: string) {
      this.collapsed[group] = !this.collapsed[group];
    },

    /**
     * @param group The group key (a container's projectGroup).
     * @returns The text shown in the group's header row.
     */
    groupLabel(group: string): string {
      // Compose project and pod names are user data; only the standalone group
      // has a fixed English key that needs a translated label.
      return group === STANDALONE_CONTAINERS_GROUP ? this.t('containers.manage.table.group.standalone') : group;
    },

    clearError() {
      switch (this.error?.source) {
      case 'namespaces': case 'containers': case 'images':
        this.SET_ERROR(undefined);
      }
    },
  },
});
</script>

<style lang="scss" scoped>
.containers {
  &-status {
    padding: 8px 5px;
  }

  .group-row {
    .group-tab {
      font-weight: bold;
      .icon {
        cursor: pointer;
      }
    }
    &[aria-expanded="false"] {
      :deep(~ .main-row) {
        visibility: collapse;
        .checkbox-container {
          /* When using visibility:collapse, the row selection checkbox produces
           * some artifacts; force it to display:none to avoid flickering. */
          display: none;
        }
      }
    }
  }
}

.dropdown {
  position: relative;
  display: inline-block;

  span {
    cursor: pointer;
    padding: 5px;
  }

  &-content {
    display: none;
    position: fixed;
    z-index: 1;
    border-start-start-radius: var(--border-radius);
    background: var(--default);
    padding: 5px;
    transition: all 0.5s ease-in-out;

    a {
      display: block;
      padding: 5px 0;
    }
  }

  &:hover {
    & > .dropdown-content {
      display: block;
    }
  }
}

.link {
  cursor: pointer;
  text-decoration: none;
}

.state-container {
  padding: 8px 5px;
  margin-top: 5px;
}

.select-namespace {
  max-width: 24rem;
  min-width: 8rem;
}

.containersTable :deep(.search-box) {
  align-self: flex-end;
}
.containersTable :deep(.bulk) {
  align-self: flex-end;
}

.container-name-link {
  color: var(--link);
  cursor: pointer;
  text-decoration: none;

  &:hover {
    text-decoration: underline;
    color: var(--link-hover);
  }
}

.port-container {
  display: flex;
  gap: 5px;
}
</style>
