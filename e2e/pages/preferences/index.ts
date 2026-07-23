import { expect, Page, Locator } from '@playwright/test';

import { ApplicationNav } from './application';
import { ContainerEngineNav } from './containerEngine';
import { KubernetesNav } from './kubernetes';
import { VirtualMachineNav } from './virtualMachine';
import { WslNav } from './wsl';

export class PreferencesPage {
  readonly page:            Page;
  readonly body:            Locator;
  readonly application:     ApplicationNav;
  readonly virtualMachine:  VirtualMachineNav;
  readonly containerEngine: ContainerEngineNav;
  readonly kubernetes:      KubernetesNav;
  readonly wsl:             WslNav;

  constructor(page: Page) {
    this.page = page;
    this.body = page.getByTestId('preferences-body');
    this.application = new ApplicationNav(page);
    this.virtualMachine = new VirtualMachineNav(page);
    this.containerEngine = new ContainerEngineNav(page);
    this.kubernetes = new KubernetesNav(page);
    this.wsl = new WslNav(page);
  }

  async waitForLoad() {
    // Wait for the navigation to appear; this only happens once the current
    // preferences have been loaded.
    await expect(this.page.locator('.preferences-nav')).toBeVisible();
  }
}
