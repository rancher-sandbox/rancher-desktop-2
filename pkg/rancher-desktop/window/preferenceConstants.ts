import _ from 'lodash';

import type { Alpha } from '@pkg/utils/typeUtils';

/**
 * prefItemData is the list of navigation items and their associated tabs for
 * the preferences window.
 */
const prefItemData = [
  {
    name: 'Application',
    tabs: ['general'/* 'behavior', 'environment' */],
  },
  // {
  //   name: process.platform === 'win32' ? 'WSL' : 'Virtual Machine',
  //   tabs: vmTabs,
  // },
  // {
  //   name: 'Container Engine',
  //   tabs: ['general'/*, 'allowed-images' */],
  // },
  { name: 'Kubernetes' },
] as const satisfies navItemEntry[];

/**
 * Represents a navigation item entry in the preferences window.
 */
interface navItemEntry {
  applicable?: () => boolean;
  name:        string;
  tabs?:       readonly string[];
}

/**
 * Represents the result of filtering navigation items
 * @returns A mapping of top level navigation item name to a list of the tabs
 * available for that navigation item.
 */
type navItemResult<T extends readonly navItemEntry[]> = {
  [K in T[number]['name']]: Omit<Extract<T[number], { name: K }>, 'applicable' | 'name'> & { index: number };
};

/**
 * Filters the navigation items based on the current platform.
 */
function filterNavItems<T extends readonly navItemEntry[]>(items: T): navItemResult<T> {
  return Object.fromEntries(items
    .filter(item => item.applicable?.() ?? true)
    .map((item, index) => [item.name, (({ name, applicable, ...rest }) => ({ ...rest, index }))(item)] as const),
  ) as navItemResult<T>;
}

export const preferencesNavItems = filterNavItems(prefItemData);

type KebabCase<T extends string> = T extends `${ infer Head }${ infer Tail }`
  ? `${ Head extends Alpha<Head> ? Lowercase<Head> : KebabCase<Tail> extends `-${ string }` ? '' : '-' }${ KebabCase<Tail> }`
  : T;

type PreferenceNavTabDefaults = {
  [K in keyof typeof preferencesNavItems as KebabCase<K>]:
  (typeof preferencesNavItems)[K] extends { tabs: readonly (infer T)[] } ? T : never;
};

/**
 * preferencesNavTabDefaults is the default navigation state for the preferences
 * window for the tabs of each top level navigation item.
 */
const preferencesNavTabDefaults = Object.fromEntries(prefItemData
  .filter((item): item is typeof item & { tabs: readonly string[] } => {
    return 'tabs' in item && item.tabs?.length > 0 && !!item.tabs[0];
  }).map(item => {
    return [_.kebabCase(item.name), item.tabs[0]] as const;
  })) as PreferenceNavTabDefaults;

/**
 * preferencesNavDefaults is the default navigation state for the preferences
 * window.  This should only be used by transient preferences.
 */
export const preferencesNavDefaults = {
  top: prefItemData[0].name as typeof prefItemData[number]['name'],
  ...preferencesNavTabDefaults,
};

/**
 * preferencesNavItemName is the type of the top level navigation item names.
 */
export type preferencesNavItemName = typeof prefItemData[number]['name'];
