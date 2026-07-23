import RDCheckbox from './checkbox';

import type { Page, Locator } from '@playwright/test';

export class KubernetesNav {
  readonly page:                          Page;
  readonly nav:                           Locator;
  readonly kubernetesToggle:              Locator;
  readonly kubernetesVersion:             Locator;
  readonly kubernetesOptions:             Locator;
  readonly kubernetesVersionLockedFields: Locator;

  constructor(page: Page) {
    this.page = page;
    this.nav = page.getByTestId('nav-kubernetes');
    this.kubernetesToggle = RDCheckbox(page.locator('[data-test="kubernetesToggle"]'));
    this.kubernetesVersion = page.locator('[data-test="kubernetesVersion"] select');
    this.kubernetesOptions = page.locator('[data-test="kubernetesOptions"]');
    this.kubernetesVersionLockedFields = page.locator('[data-test="kubernetesVersion"] > .select-k8s-version > .icon-lock');
  }
}
