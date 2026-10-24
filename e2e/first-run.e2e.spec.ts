import { test, expect } from '@playwright/test';

import FirstRunPage from './pages/first-run-page';
import { startRancherDesktop } from './utils/TestUtils';

import type { ElectronApplication } from '@playwright/test';

test.describe.configure({ mode: 'serial' });

test.describe('First Run Dialog', () => {
  let electronApp: ElectronApplication;
  let firstRunPage: FirstRunPage;

  test.beforeAll(async({ colorScheme }, testInfo) => {
    electronApp = await startRancherDesktop(testInfo, { firstRun: true });
    firstRunPage = new FirstRunPage(await electronApp.firstWindow());
  });

  test('should load the first run dialog', async() => {
    await expect.poll(() => firstRunPage.page.url()).toContain('FirstRun');
    expect(firstRunPage.page.url()).toContain('FirstRun');
  });

  test('should enable the accept button', async() => {
    await expect(firstRunPage.acceptButton).toBeEnabled();
  });

  test('should have Kubernetes disabled by default', async() => {
    await expect(firstRunPage.kubernetesEnabled).toBeVisible();
    await expect(firstRunPage.kubernetesEnabled).not.toBeChecked();
    await expect(firstRunPage.kubernetesVersion).toBeVisible();
    await expect(firstRunPage.kubernetesVersion).toBeDisabled();
  });

  test('should allow enabling Kubernetes', async() => {
    await firstRunPage.kubernetesEnabled.check();
    await expect(firstRunPage.kubernetesEnabled).toBeChecked();
    // Don't check if the versions are enabled: that needs to wait for the
    // versions config map to be populated.
  });

  test('should support selecting container engine', async() => {
    await expect(firstRunPage.engineSelector).toBeVisible();
    await expect(firstRunPage.engineSelector).toBeEnabled();
    await expect(firstRunPage.engineSelector).toHaveAttribute('data-value', 'moby');
  });

  test('should close window when accepted', async() => {
    const nextWindow = electronApp.waitForEvent('window');
    await firstRunPage.acceptButton.click();
    await expect.poll(() => firstRunPage.page.isClosed()).toBe(true);
    expect(await nextWindow).toHaveURL(/General/);
  });
});
