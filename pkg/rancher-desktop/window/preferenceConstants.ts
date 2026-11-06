import type { KebabCase } from '@pkg/utils/string-utils';

/**
 * prefItemData is the list of navigation items and their associated tabs for
 * the preferences window.
 */
const prefItemData = defineNavItems([
  {
    name:     'application',
    labelKey: 'preferences.nav.application',
    tabs:     [
      ['general', 'preferences.tabs.general'],
      // ['behavior', 'preferences.tabs.behavior'],
      ['environment', 'preferences.tabs.environment'],
    ],
  },
  {
    // For now, show the Virtual Machine tab on all platforms, per
    // https://github.com/rancher-sandbox/rancher-desktop-2/issues/581
    // applicable: () => process.platform === 'darwin' || process.platform === 'linux',
    name:      'virtual-machine',
    labelKey: 'preferences.nav.virtualMachine',
    tabs:      [
      ['hardware', 'preferences.tabs.hardware'],
      // ['volumes', 'preferences.tabs.volumes'],
      // ['network', 'preferences.tabs.network'],
      // ['emulation', 'preferences.tabs.emulation'],
    ],
  },
  // {
  //   applicable: () => process.platform === 'win32',
  //   name:      'wsl',
  //   labelKey:  'preferences.nav.wsl',
  //   tabs:      [
  //     ['integrations', 'preferences.tabs.integrations'],
  //     ['network', 'preferences.tabs.network'],
  //     ['proxy', 'preferences.tabs.proxy'],
  //   ],
  // },
  {
    name:     'container-engine',
    labelKey: 'preferences.nav.containerEngine',
    tabs:     [
      ['general', 'preferences.tabs.general'],
      // ['allowed-images', 'preferences.tabs.allowedImages'],
    ],
  },
  { name: 'kubernetes', labelKey: 'preferences.nav.kubernetes' },
] as const);

/**
 * Represents a navigation item entry in the preferences window.
 */
interface navItemEntry {
  applicable?: () => boolean;
  name:        string;
  labelKey:    string;
  tabs?:       readonly [string, string][];
}

/**
 * EnforceKebabNames is a type that ensures that the following properties of
 * each navigation item is in kebab case:
 * - The `name` property of the navigation item.
 * - The first element of each tuple in the `tabs` property of the navigation item.
 */
type EnforceKebabNames<T extends readonly navItemEntry[]> = {
  [I in keyof T]:
  T[I] extends { name: infer N extends string }
    ? Omit<T[I], 'name' | 'tabs'> & {
      name: KebabCase<N>;
    } & (
        T[I] extends { tabs: infer Tabs extends readonly unknown[] }
          ? {
            tabs: {
              [J in keyof Tabs]:
              Tabs[J] extends [infer U extends string, infer V extends string]
                ? [KebabCase<U>, V]
                : never
            }
          }
          : unknown
      )
    : T[I];
};

/**
 * defineNavItems is a helper function that enforces the correct types for
 * {@link prefItemData}.
 */
function defineNavItems<const T extends readonly navItemEntry[]>(
  items: EnforceKebabNames<T>,
): EnforceKebabNames<T> {
  return items;
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
 * Given filtered navigation items, returns a mapping of top level navigation
 * item name to a list of the tabs available for that navigation item.
 */
function mapNavItems<T extends readonly navItemEntry[]>(items: T): navItemResult<T> {
  return Object.fromEntries(items
    .map((item, index) => [item.name, (({ name, applicable, ...rest }) => ({ ...rest, index }))(item)] as const),
  ) as navItemResult<T>;
}

export const preferencesNavItems = prefItemData.filter((item: navItemEntry) =>
  item.applicable?.() ?? true);

export const preferencesNavItemsMap = mapNavItems(preferencesNavItems);

type PreferenceNavTabDefaults = {
  [K in keyof typeof preferencesNavItemsMap]:
  (typeof preferencesNavItemsMap)[K] extends { tabs: readonly ([infer T, string])[] } ? T : never;
};

/**
 * preferencesNavTabDefaults is the default navigation state for the preferences
 * window for the tabs of each top level navigation item.
 * @note For pages with no tabs, the type will be `page-name: never`.
 */
const preferencesNavTabDefaults = Object.fromEntries(prefItemData
  .filter((item): item is typeof item & { tabs: readonly ([string, string])[] } => {
    return 'tabs' in item && item.tabs?.length > 0 && !!item.tabs[0];
  }).map(item => {
    return [item.name, item.tabs[0][0]] as const;
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
 * preferencesNavItem is the type of the top level navigation items.
 */
export type preferencesNavItem = typeof prefItemData[number];

/**
 * preferencesNavItemName is the type of the top level navigation item names.
 */
export type preferencesNavItemName = preferencesNavItem['name'];
