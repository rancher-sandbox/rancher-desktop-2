<template>
  <PortForwarding
    class="content"
    :services="services"
    :include-kubernetes-services="settings.portForwarding.includeKubernetesServices"
    :k8s-state="state"
    :kubernetes-is-disabled="!settings.kubernetes.enabled"
    :service-being-edited="serviceBeingEdited"
    :error-message="errorMessage"
    @update-port="handleUpdatePort"
    @toggled-service-filter="onIncludeK8sServicesChanged"
    @edit-port-forward="handleEditPortForward"
    @cancel-port-forward="handleCancelPortForward"
    @cancel-edit-port-forward="handleCancelEditPortForward"
    @update-port-forward="handleUpdatePortForward"
    @close-error="handleCloseError"
  />
</template>

<script lang="ts">

import { defineComponent } from 'vue';

import PortForwarding from '@pkg/components/PortForwarding.vue';
import { defaultSettings } from '@pkg/config/settings';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';

export default defineComponent({
  name:       'port-forwarding',
  components: { PortForwarding },
  data() {
    return {
      state:              'STARTED',
      settings:           defaultSettings,
      services:           [] as any[],
      errorMessage:       undefined as string | undefined,
      serviceBeingEdited: undefined as any | undefined,
    };
  },

  watch: {
    services: {
      handler(newServices: any[]): void {
        if (this.serviceBeingEdited) {
          const newService = newServices.find(service => this.compareServices(this.serviceBeingEdited, service));

          if (newService) {
            this.serviceBeingEdited = Object.assign(this.serviceBeingEdited, { listenPort: newService.listenPort });
          }
        }
      },
      deep: true,
    },
  },

  mounted() {
    this.$store.dispatch(
      'page/setHeader',
      { title: this.t('portForwarding.title') },
    );
    ipcRenderer.on('settings-update', (event, settings) => {
      // TODO: put in a status bar
      this.$data.settings = settings;
    });
    ipcRenderer.on('settings-read', (event, settings) => {
      this.$data.settings = settings;
    });
    ipcRenderer.send('settings-read');
  },

  methods: {
    handleUpdatePort(newPort: number): void {
      if (this.serviceBeingEdited) {
        this.serviceBeingEdited.listenPort = newPort;
      }
    },

    onIncludeK8sServicesChanged(value: boolean): void {
      if (value !== this.settings.portForwarding.includeKubernetesServices) {
        ipcRenderer.invoke('settings-write',
          { portForwarding: { includeKubernetesServices: value } } );
      }
    },

    compareServices(service1: any, service2: any): boolean {
      return service1.name === service2.name &&
        service1.namespace === service2.namespace &&
        service1.port === service2.port;
    },

    findServiceMatching(serviceToMatch: any | undefined, serviceList: any[]): any | undefined {
      if (!serviceToMatch) {
        return undefined;
      }
      const compareServices = (service1: any, service2: any) => {
        return service1.name === service2.name &&
          service1.namespace === service2.namespace &&
          service1.port === service2.port;
      };

      return serviceList.find(service => compareServices(service, serviceToMatch));
    },

    handleEditPortForward(service: any): void {
      // TODO: Implement.
    },

    handleCancelEditPortForward(service: any): void {
      // TODO: Implement.
    },

    handleCancelPortForward(service: any): void {
      // TODO: Implement.
    },

    handleUpdatePortForward(): void {
      // TODO: Implement.
    },

    handleCloseError(): void {
      this.errorMessage = undefined;
    },
  },
});
</script>

<style scoped>
  .content {
    padding-top: 13px;
  }
</style>
