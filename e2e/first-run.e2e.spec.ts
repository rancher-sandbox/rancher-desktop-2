import { test, expect } from '@playwright/test';

import FirstRunPage from './pages/first-run-page';
import { startRancherDesktop, teardown } from './utils/TestUtils';

import { IoRancherdesktopAppV1alpha1AppSpecApplicationAddPathEnum as AddPath } from '@rdd-client';

import type { ElectronApplication } from '@playwright/test';

test.describe.configure({ mode: 'serial' });

test.describe('First Run Dialog', () => {
  let electronApp: ElectronApplication;
  let firstRunPage: FirstRunPage;

  test.beforeAll(async({ colorScheme }, testInfo) => {
    electronApp = await startRancherDesktop(testInfo, { firstRun: true });
    firstRunPage = new FirstRunPage(await electronApp.firstWindow());
  });

  test.afterAll(({ colorScheme }, testInfo) => teardown(electronApp, testInfo));

  test('should load the first run dialog', async() => {
    await expect.poll(() => firstRunPage.page.url()).toContain('FirstRun');
  });

  test('should enable the accept button', async() => {
    await expect(firstRunPage.acceptButton).toBeEnabled();
  });

  test('should have Kubernetes disabled by default', async() => {
    await expect(firstRunPage.kubernetesEnabled).toBeVisible();
    await expect(firstRunPage.kubernetesEnabled.checkbox).not.toBeChecked();
    await expect(firstRunPage.kubernetesVersion).toBeVisible();
    // This is the placeholder <select>.
    await expect(firstRunPage.kubernetesVersion).toBeDisabled();
  });

  test('should allow enabling Kubernetes', async() => {
    await firstRunPage.kubernetesEnabled.check();
    await expect(firstRunPage.kubernetesEnabled.checkbox).toBeChecked();
    // Don't check if the versions are enabled: that needs to wait for the
    // versions config map to be populated.
  });

  test('should support selecting container engine', async() => {
    await expect(firstRunPage.engineSelector).toBeVisible();
    for (const radio of await firstRunPage.engineSelector.locator('input[type="radio"]').all()) {
      await expect(radio).toBeEnabled();
    }
    await expect(firstRunPage.engineSelector).toHaveAttribute('data-value', 'moby');
  });

  test('should allow selecting path management', async() => {
    await expect(firstRunPage.pathManagementSelector).toBeVisible();
    for (const value of Object.values(AddPath)) {
      const radio = firstRunPage.pathManagementSelector.getByTestId(`path-management-${ value }`);
      await expect(radio).toBeVisible();
      await expect(radio.locator('input[type="radio"]')).toBeEnabled();
      await radio.click();
      await expect(firstRunPage.pathManagementSelector).toHaveAttribute('data-value', value);
    }
  });

  test('should close window when accepted', async() => {
    const nextWindow = electronApp.waitForEvent('window');
    await firstRunPage.acceptButton.click();
    await expect.poll(() => firstRunPage.page.isClosed()).toBe(true);
    await expect(await nextWindow).not.toHaveURL(/FirstRun/);
  });
});
