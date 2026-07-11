import type { preferencesNavItemName } from '@pkg/window/preferenceConstants';

/**
 * TransientPreferencesState is the state for the transient preferences; it
 * holds preferences that are reset when the application exits.
 */
export type TransientPreferencesState = typeof defaultTransientPreferences;

/**
 * defaultTransientPreferences is the initial transient preferences state on
 * application startup.
 */
const defaultTransientPreferences = {
  /** navigation state, e.g. which tab is selected */
  navigation: {
    preferences: {
      top:                'Kubernetes' as preferencesNavItemName,
      application:        'general' as 'general' | 'behavior' | 'environment',
      'virtual-machine':  'hardware' as 'hardware' | 'volumes' | 'emulation',
      wsl:                'integrations' as 'integrations' | 'proxy',
      'container-engine': 'general' as 'general' | 'allowed-images',
    },
  },
};

export default defaultTransientPreferences;
