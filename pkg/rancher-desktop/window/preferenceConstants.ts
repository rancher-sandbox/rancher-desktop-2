export const preferencesNavItems = [
  /*
  {
    name: 'Application',
    tabs: ['general', 'behavior', 'environment'],
  },
  */
  /*
  {
    name: process.platform === 'win32' ? 'WSL' : 'Virtual Machine',
    tabs: vmTabs,
  },
  */
  {
    name: 'Container Engine',
    tabs: ['general'/*, 'allowed-images' */],
  },
  { name: 'Kubernetes' },
] as const satisfies readonly { name: string; tabs?: readonly string[] }[];
export type preferencesNavItemName = (typeof preferencesNavItems)[number]['name'];
