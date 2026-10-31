import { importTypes } from '@rancher/auto-import';
import { IInternal, IPlugin } from '@shell/core/types';
import Socket from '@shell/utils/socket';

// Init the package
export default function(plugin: IPlugin, internal: IInternal): void {
  // Auto-import model, detail, edit from the folders
  importTypes(plugin);

  // Provide plugin metadata from package.json
  plugin.metadata = require('./package.json');

  const { $axios, store, app: { router } } = internal;

  interceptApiRequest($axios);
  interceptWebSocketUrls();

  const logoRoute = router.resolve({
    name: 'c-cluster-explorer',
    params: { cluster: "local" },
  });

  store.commit('setIsSingleProduct', {
    productNameKey: 'rancher-desktop.label',
    logoRoute,
    logo: require(`./assets/logo.svg`),
  });

  // Hide buttons in the header that don't work in Rancher Desktop.
  // KubeConfig buttons don't function (rancher-sandbox/rancher-desktop#2208),
  // and Kubectl Shell is not useful since users can use a local terminal
  // (rancher-sandbox/rancher-desktop#8151).
  store.commit('type-map/product', {
    name:           'explorer',
    hideKubeShell:  true,
    hideKubeConfig: true,
    hideCopyConfig: true,
  });
}

/**
 * Intercepts requests to rewrite URLs. This is useful intercepting any direct
 * API calls when running dashboard on plain HTTP.
 *
 * NOTE: This is currently used for running Dashboard in Rancher Desktop.
 * @param {*} axios The axios instance to modify
 */
const interceptApiRequest = (axios: any) => {
  axios.interceptors.request.use((config: any) => {
    // ensure that traffic to port 6120 stays on http
    if (config.url.includes(':6120')) {
      config.url = config.url
        .replace('https://', 'http://');
    }

    return config;
  }, (error: any) => {
    return Promise.reject(error);
  });
};

/**
 * Intercepts WebSocket URL construction to rewrite URLs for the HTTP port.
 *
 * rdd serves the dashboard over plain HTTP, so WebSocket connections for pod
 * logs/shell need to use ws:// (not wss://).
 *
 * This fixes issue #3212 where newly created pods fail to show logs because
 * their WebSocket URLs use wss:// on the HTTP port.
 */
const interceptWebSocketUrls = () => {
  const originalSetUrl = Socket.prototype.setUrl;

  Socket.prototype.setUrl = function(url: string) {
    // Let the original setUrl process the URL first (handles relative URLs,
    // protocol upgrades, etc.)
    originalSetUrl.call(this, url);

    // Now apply our transformations to this.url AFTER the original processing.
    // The original setUrl upgrades ws:// to wss:// when served over HTTPS,
    // so we need to fix it here.

    // Convert wss://127.0.0.1:6120 → ws://127.0.0.1:6120
    if (this.url.includes(':6120')) {
      this.url = this.url.replace('wss://', 'ws://');
    }
  };
};
