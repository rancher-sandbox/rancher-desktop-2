import { test, expect, _electron } from '@playwright/test';

import { NavPage } from './pages/nav-page';
import { rdd, startRancherDesktop, teardown } from './utils/TestUtils';

import * as rddClient from '@rdd-client';

import type { ElectronApplication, Page } from '@playwright/test';

test.describe.serial('Main App Test', () => {
  let electronApp: ElectronApplication;
  let page: Page;

  test.beforeAll(async({ colorScheme }, testInfo) => {
    electronApp = await startRancherDesktop(testInfo);
    page = await electronApp.firstWindow();
  });

  test.afterAll(({ colorScheme }, testInfo) => teardown(electronApp, testInfo));

  test('should start loading the background services and hide progress bar', async() => {
    const navPage = new NavPage(page);

    await navPage.waitForAppSettled();
    await expect(navPage.progressBar).toBeHidden();
  });

  test('should land on General page', async() => {
    const navPage = new NavPage(page);

    await expect(navPage.mainTitle).toHaveText('Welcome to Rancher Desktop 2 by SUSE');
  });

  test('should navigate to Images page', async() => {
    const navPage = new NavPage(page);
    const imagesPage = await navPage.navigateTo('Images');

    await expect(navPage.mainTitle).toHaveText('Images');
    await expect(imagesPage.table).toBeVisible();
  });

  test('should navigate back to the General page', async() => {
    const navPage = new NavPage(page);
    await navPage.navigateTo('General');

    await expect(navPage.mainTitle).toHaveText('Welcome to Rancher Desktop 2 by SUSE');
  });

  test('application should have been created', async() => {
    const rawApps = await rdd('ctl', 'get', 'apps', '--output=json');
    const appList: rddClient.IoRancherdesktopAppV1alpha1AppList = JSON.parse(rawApps);
    expect(appList.items).toHaveLength(1);
    const app = appList.items[0];
    expect(app.spec?.running).toBe(true);
  });

  test('progress should return when application not settled', async() => {
    const navPage = new NavPage(page);

    await rdd('set', 'running=false', '--wait=false');
    await expect(navPage.progressBar).toBeVisible();
    // The text should be reflected from the status condition.
    const rawApps = await rdd('ctl', 'get', 'apps', '--output=json');
    const appList: rddClient.IoRancherdesktopAppV1alpha1AppList = JSON.parse(rawApps);
    expect(appList.items).toHaveLength(1);
    const condition = appList.items[0].status?.conditions?.find((c) => c.type === 'Settled');
    await expect(navPage.progressBar).toHaveText(condition?.message ?? '<missing>');
  });
});
