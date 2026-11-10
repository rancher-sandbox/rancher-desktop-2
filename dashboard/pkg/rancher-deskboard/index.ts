import { importTypes } from '@rancher/auto-import';
import { IInternal, IPlugin } from '@shell/core/types';

// Init the package
export default function(plugin: IPlugin, internal: IInternal): void {
  // Auto-import model, detail, edit from the folders
  importTypes(plugin);

  // Provide plugin metadata from package.json
  plugin.metadata = require('./package.json');

  const { store, app: { router } } = internal;

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
