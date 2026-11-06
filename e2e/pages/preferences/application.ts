import type { Page, Locator } from '@playwright/test';
export class ApplicationNav {
  readonly page:                     Page;
  readonly nav:                      Locator;
  readonly tabGeneral:               Locator;
  readonly tabEnvironment:           Locator;
  readonly tabBehavior:              Locator;
  readonly tabPanel:                 Locator;
  readonly localeSelect:             Locator;
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
    this.tabGeneral = page.getByTestId('btn-general');
    this.tabEnvironment = page.getByTestId('btn-environment');
    this.tabBehavior = page.getByTestId('btn-behavior');
    this.tabPanel = page.getByRole('tabpanel');
    // General tab
    this.localeSelect = page.getByTestId('localeSelect');
    this.administrativeAccess = page.getByTestId('administrativeAccess');
    this.automaticUpdates = page.getByTestId('automaticUpdates');
    this.automaticUpdatesCheckbox = page.getByTestId('automaticUpdatesCheckbox');
    this.statistics = page.getByTestId('statistics');
    // Behavior tab
    this.autoStart = page.getByTestId('autoStart');
    this.background = page.getByTestId('background');
    this.notificationIcon = page.getByTestId('notificationIcon');
    // Environment tab
    this.pathManagement = page.getByTestId('pathManagement');
  }
}
