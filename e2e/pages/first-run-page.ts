import { Locator, Page } from '@playwright/test';

import RDCheckbox from './preferences/checkbox';

export default class FirstRunPage {
  readonly page:              Page;
  readonly kubernetesEnabled: Locator;
  readonly kubernetesVersion: Locator;
  readonly engineSelector:    Locator;
  readonly acceptButton:      Locator;

  constructor(page: Page) {
    this.page = page;
    this.kubernetesEnabled = RDCheckbox(page.getByTestId('kubernetes-enabled'));
    this.kubernetesVersion = page.getByTestId('kubernetes-version').locator('select');
    this.engineSelector = page.getByTestId('engine-selector');
    this.acceptButton = page.getByTestId('accept-btn');
  }
}
