import { shell } from 'electron';

import mainEvents from '@pkg/main/mainEvents';
import type { TransientPreferencesState } from '@pkg/types/transientPreferences';

type PrefNav = TransientPreferencesState['navigation']['preferences'];
/** TopKey is any key for the top-level preferences. */
type TopKey = Exclude<keyof PrefNav, 'top'>;
/** TabKey is the preference mapping key, as `<top>/<inner tab>`. */
type TabKey = {
  [K in TopKey]:
  [PrefNav[K]] extends [never]
    ? K
    : `${ K }/${ PrefNav[K] }`
}[TopKey];

const baseUrl = process.env.RD_DOCS_URL ?? 'https://docs.rancherdesktop.io';

class PreferencesHelp {
  private readonly mapping = {
    // 'application/behavior':            'ui/preferences/application/behavior',
    // 'application/environment':         'ui/preferences/application/environment',
    'application/general':             'ui/preferences/application/general',
    'virtual-machine/hardware':        'ui/preferences/virtual-machine/hardware',
    // 'virtual-machine/volumes':         'ui/preferences/virtual-machine/volumes',
    // 'virtual-machine/emulation':       'ui/preferences/virtual-machine/emulation',
    // 'container-engine/general':        'ui/preferences/container-engine/general',
    // 'container-engine/allowed-images': 'ui/preferences/container-engine/allowed-images',
    // 'wsl/integrations':                'ui/preferences/wsl/integrations',
    // 'wsl/proxy':                       'ui/preferences/wsl/proxy',
    kubernetes:                        'ui/preferences/kubernetes',
  } as const satisfies Record<TabKey, `ui/preferences/${ string }`>;

  async openUrl() {
    const transientPreferences = await mainEvents.invoke('transient-preferences/get');
    const { preferences } = transientPreferences.navigation;
    const { top } = preferences;
    type topKeys = Exclude<keyof typeof preferences, 'top'>;
    const current = top.toLowerCase().replace(/ /g, '-') as topKeys;
    const tab = current in preferences ? `/${ preferences[current] }` as const : '';
    const key = `${ current }${ tab }` as const;
    let url = baseUrl;

    if (key in this.mapping) {
      url += `/${ this.mapping[key as keyof typeof this.mapping] }`;
    }
    await shell.openExternal(url);
  }
}

export const Help = {
  preferences: new PreferencesHelp(),
  openUrl() {
    shell.openExternal(baseUrl);
  },
};
