import type { Page, Locator } from '@playwright/test';
export class ApplicationNav {
  readonly page:                     Page;
  readonly nav:                      Locator;
  readonly tabBehavior:              Locator;
  readonly tabEnvironment:           Locator;
  readonly tabGeneral:               Locator;
  readonly administrativeAccess:     Locator;
  readonly automaticUpdates:         Locator;
  readonly automaticUpdatesCheckbox: Locator;
  readonly statistics:               Locator;
  readonly autoStart:                Locator;
  readonly background:               Locator;
  readonly notificationIcon:         Locator;
  readonly pathManagement:           Locator;

  constructor(page: Page) {
    this.page = page;
    this.nav = page.getByTestId('nav-application');
    this.tabBehavior = page.getByTestId('btn-behavior');
    this.tabEnvironment = page.getByTestId('btn-environment');
    this.tabGeneral = page.getByTestId('btn-general');
    this.administrativeAccess = page.getByTestId('administrativeAccess');
    this.automaticUpdates = page.getByTestId('automaticUpdates');
    this.automaticUpdatesCheckbox = page.getByTestId('automaticUpdatesCheckbox');
    this.statistics = page.getByTestId('statistics');
    this.autoStart = page.getByTestId('autoStart');
    this.background = page.getByTestId('background');
    this.notificationIcon = page.getByTestId('notificationIcon');
    this.pathManagement = page.getByTestId('pathManagement');
  }
}
