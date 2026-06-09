import { computed } from 'vue';
import { useStore } from 'vuex';

/**
 * ControllerManagerInfo describes the data stored in the rdd-controller-manager
 * ConfigMap; this is used to determine the endpoints for the passthroughs.
 */
interface ControllerManagerInfo {
  healthPort:          number;
  metricsPort:         number;
  enabledControllers:  string[];
  enabledPassthroughs: Record<string, string[]>;
  startTime:           string;
  healthEndpoint:      string;
  metricsEndpoint:     string;
  passthroughEndpoint: string;
}

let systemConfigMapsWatched = false;

/**
 * usePassthroughURL calculates the URL for a given passthrough endpoint.
 */
export function usePassthroughURL(endpoint: string) {
  const store = useStore();

  // Ensure that we're watching the systemConfigMaps.  We never unwatch it.
  if (!systemConfigMapsWatched) {
    // Temporarily set the flag synchronously; if it fails, we reset it again.
    systemConfigMapsWatched = true;
    store.dispatch('rdd/watchResources', ['systemConfigMaps']).catch(err => {
      systemConfigMapsWatched = false;
      console.error('Error watching systemConfigMaps:', err);
    });
  }

  const kubeConfig = computed(() => store.state['rdd-connection'].config);
  const serverURL = computed(() =>
    (url => url ? new URL(url) : undefined)(kubeConfig.value?.getCurrentCluster()?.server));
  const configMap = computed(() =>
    store.state.rdd.systemConfigMaps?.find(cm => cm.metadata?.name === 'rdd-controller-manager'));
  const info = computed(() => Object.values(configMap.value?.data ?? {})
    .map(entry => {
      try {
        return JSON.parse(entry) as ControllerManagerInfo;
      } catch (ex) {
        return undefined;
      }
    }).map(info => info?.enabledPassthroughs ?? {}));
  const passthroughs = computed(() => Object.assign({}, ...info.value) as typeof info.value[number]);
  const controller = computed(() => {
    // Find the controllers that have the endpoint enabled.
    const controllers = new Set(
      Object.entries(passthroughs.value ?? {})
        .filter(([_, endpoints]) => endpoints.includes(endpoint))
        .map(([controller, _]) => controller));

    // If the mock controller is enabled, use it.
    if (controllers.has('mock')) {
      return 'mock';
    }

    // Otherwise, pick a random one.
    return controllers.values().next().value;
  });
  return computed(() => {
    if (!serverURL.value || !controller.value) {
      return undefined;
    }
    return new URL(`/passthrough/${ controller.value }/${ endpoint }/`, serverURL.value).href;
  });
}
