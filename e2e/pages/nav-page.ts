import { expect, Locator, Page } from '@playwright/test';

import { ContainersPage } from './containers-page';
import { DiagnosticsPage } from './diagnostics-page';
import { ExtensionsPage } from './extensions-page';
import { ImagesPage } from './images-page';
import { K8sPage } from './k8s-page';
import { PortForwardPage } from './portforward-page';
import { SnapshotsPage } from './snapshots-page';
import { TroubleshootingPage } from './troubleshooting-page';
import { VolumesPage } from './volumes-page';
import { WSLIntegrationsPage } from './wsl-integrations-page';
import { rdd } from '../utils/TestUtils';

import * as rddClient from '@rdd-client';

const pageConstructors = {
  General:         (page: Page) => page,
  K8s:             (page: Page) => new K8sPage(page),
  WSLIntegrations: (page: Page) => new WSLIntegrationsPage(page),
  Containers:      (page: Page) => new ContainersPage(page),
  PortForwarding:  (page: Page) => new PortForwardPage(page),
  Images:          (page: Page) => new ImagesPage(page),
  Troubleshooting: (page: Page) => new TroubleshootingPage(page),
  Snapshots:       (page: Page) => new SnapshotsPage(page),
  Diagnostics:     (page: Page) => new DiagnosticsPage(page),
  Extensions:      (page: Page) => new ExtensionsPage(page),
  Volumes:         (page: Page) => new VolumesPage(page),
};

export class NavPage {
  readonly page:              Page;
  readonly progressBar:       Locator;
  readonly mainTitle:         Locator;
  readonly dashboardButton:   Locator;
  readonly preferencesButton: Locator;

  constructor(page: Page) {
    this.page = page;
    this.mainTitle = page.locator('[data-test="mainTitle"]');
    this.progressBar = page.locator('.progress');
    this.dashboardButton = page.getByTestId('dashboard-button');
    this.preferencesButton = page.getByTestId('preferences-button');
  }

  protected async isAppSettled(): Promise<boolean> {
    // Check CRDs first, to avoid error messages when apps are not registered yet.
    const AppsCRDSuffix = '/apps.app.rancherdesktop.io';
    const crds = await rdd('ctl', 'get', 'crds', '--output=name');
    if (!crds.split('\n').some(line => line.trim().endsWith(AppsCRDSuffix))) {
      return false;
    }
    const rawApps = await rdd('ctl', 'get', 'apps', '--output=json');
    const appList: rddClient.IoRancherdesktopAppV1alpha1AppList = JSON.parse(rawApps);
    const conditions = appList.items.flatMap(item => item.status?.conditions ?? []);
    return conditions.some(condition => condition.type === 'Settled' && condition.status === 'True');
  }

  /**
   * Wait for the backend app to be settled.
   */
  async waitForAppSettled() {
    // We are using the mock controller, so the progress should become ready
    // fairly quickly.
    await expect.poll(() => this.isAppSettled(), {
      timeout: 30_000,
      message: 'Backend did not settle',
    }).toBeTruthy();
  }

  /**
   * Navigate to a given tab, returning the page object model appropriate for
   * the destination tab.
   */
  async navigateTo<pageName extends keyof typeof pageConstructors>(tab: pageName):
  Promise<ReturnType<typeof pageConstructors[pageName]>>;

  async navigateTo(tab: keyof typeof pageConstructors) {
    const pageLoadHooks: Partial<Record<keyof typeof pageConstructors, (this: NavPage) => Promise<unknown>>> = {
      Extensions: async function() { await this.page.waitForSelector('.extensions-page', { timeout: 60_000 }) },
    };

    await this.page.click(`.nav li[item="/${ tab }"] a`);
    await this.page.waitForURL(`**/${ tab }*`, { timeout: 60_000 });
    await pageLoadHooks[tab]?.call(this);

    return pageConstructors[tab](this.page);
  }
}
