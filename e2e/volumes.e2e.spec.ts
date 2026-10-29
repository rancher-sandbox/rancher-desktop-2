import { ElectronApplication, Page, expect, test } from '@playwright/test';

import { NavPage } from './pages/nav-page';
import { VolumesPage } from './pages/volumes-page';
import { getRDDConfig, startSlowerDesktop, teardown, tool } from './utils/TestUtils';

import { APP_NAMESPACE } from '@/pkg/rancher-desktop/utils/rdd';
import mockVolumes from '@/rdd/pkg/controllers/mock/testdata/volumes.json' with { type: 'json' };
import * as RDDClient from '@rdd-client';

/** Button indexes in the delete confirmation, matching its `buttons` array. */
const CONFIRM_DELETE = 0;
const CANCEL_DELETE = 1;

/**
 * Replace the next native message box with one that answers `response` and
 * records the options it was given, so a test can assert what the user saw.
 */
async function answerMessageBox(electronApp: ElectronApplication, response: number) {
  await electronApp.evaluate(({ dialog }, response) => {
    let resolve: (value: Electron.MessageBoxOptions) => void;
    (globalThis as any).lastMessageBox = new Promise<Electron.MessageBoxOptions>(_resolve => { resolve = _resolve });
    const original = dialog.showMessageBox;
    dialog.showMessageBox = (windowOrOptions, maybeOptions?: Electron.MessageBoxOptions) => {
      const options = maybeOptions ?? windowOrOptions as Electron.MessageBoxOptions;
      dialog.showMessageBox = original;
      resolve(options);

      return Promise.resolve({ response, checkboxChecked: false });
    };
  }, response);
}

/** The options the last message box was shown with. */
async function lastMessageBox(electronApp: ElectronApplication) {
  return await electronApp.evaluate(() => Promise.race([
    (globalThis as any).lastMessageBox,
    new Promise((_resolve, reject) => setTimeout(
      () => reject(new Error('Timeout waiting for last message box')),
      10_000)),
  ]));
}

let page: Page;

test.describe.configure({ mode: 'serial' });

test.describe('Volumes Tests', () => {
  let electronApp: ElectronApplication;
  let client: RDDClient.ContainersRancherdesktopIoV1alpha1Api;
  let rddConfigCleanup = () => {};

  // The mock backend starts with an initial set of static volumes; we can then
  // delete them, or create them under the same name.  It is not possible to
  // create new volumes that are unknown to it.  We use the first volume found
  // to test single-volume operations, and the rest for multi-volume operations.
  const testVolumeName = mockVolumes[0].Name;
  const bulkVolumeNames = mockVolumes.slice(1).map(volume => volume.Name);

  test.beforeAll(async({ colorScheme }, testInfo) => {
    [electronApp, page] = await startSlowerDesktop(testInfo);
    const [rddConfig, cleanup] = await getRDDConfig();

    rddConfigCleanup = cleanup;
    client = rddConfig.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    const navPage = new NavPage(page);

    await navPage.waitForAppSettled();
  });

  test.afterAll(async({ colorScheme }, testInfo) => {
    await teardown(electronApp, testInfo);
    rddConfigCleanup();
  });

  test('should navigate to volumes page', async() => {
    const navPage = new NavPage(page);
    const volumesPage = await navPage.navigateTo('Volumes');

    await expect(navPage.mainTitle).toHaveText('Volumes');
    await volumesPage.waitForTableToLoad();
  });

  test('should display volume in the list', async() => {
    const volumesPage = new VolumesPage(page);

    await volumesPage.waitForVolumeToAppear(testVolumeName);

    // Cross-check against the RDD API directly, not just the GUI's own state.
    const volumes = await client.listVolumeForAllNamespaces();

    expect(volumes.items.some(volume => volume.status?.name === testVolumeName)).toBe(true);
  });

  test('should show volume information', async() => {
    const volumesPage = new VolumesPage(page);
    const volumeInfo = volumesPage.getVolumeInfo(testVolumeName);

    await expect(volumeInfo.name).toHaveText(testVolumeName);
    await expect(volumeInfo.driver).not.toBeEmpty();
    await expect(volumeInfo.mountpoint).not.toBeEmpty();
  });

  test.fixme('should browse volume files', async() => {
    const volumesPage = new VolumesPage(page);

    await volumesPage.browseVolumeFiles(testVolumeName);

    await page.waitForURL(`**/volumes/files/${ testVolumeName }`, {
      timeout: 10_000,
    });

    await page.goBack();
    await volumesPage.waitForTableToLoad();
  });

  test('should handle search functionality', async() => {
    const volumesPage = new VolumesPage(page);

    await volumesPage.searchVolumes(testVolumeName);
    await expect(volumesPage.getVolumeRow(testVolumeName)).toBeVisible();

    await volumesPage.searchVolumes('this-volume-does-not-exist');
    await expect(volumesPage.getVolumeRow(testVolumeName)).toBeHidden();

    // Clear the search so later tests see the full, unfiltered list again.
    await volumesPage.searchVolumes('');
    await expect(volumesPage.getVolumeRow(testVolumeName)).toBeVisible();
  });

  test('should allow cancelling delete', async() => {
    const volumesPage = new VolumesPage(page);

    // Disable the electron dialog box for deletion confirmation.
    await answerMessageBox(electronApp, CANCEL_DELETE);

    await volumesPage.waitForVolumeToAppear(testVolumeName);
    await volumesPage.deleteVolume(testVolumeName);
    await expect(lastMessageBox(electronApp)).resolves.toMatchObject({
      message: 'Delete 1 volume?',
      detail:  expect.stringContaining(testVolumeName),
    });

    // Reload to ensure that the deletion was not applied.
    await page.reload();
    await volumesPage.waitForTableToLoad();
    await expect(volumesPage.getVolumeRow(testVolumeName)).toBeVisible();
  });

  test('should auto-refresh volumes list', async() => {
    const volumesPage = new VolumesPage(page);

    // Disable the electron dialog box for deletion confirmation.
    await answerMessageBox(electronApp, CONFIRM_DELETE);

    await volumesPage.waitForVolumeToAppear(testVolumeName);
    await expect(volumesPage.errorBanner).toBeHidden();
    await volumesPage.deleteVolume(testVolumeName);
    await expect(volumesPage.errorBanner).toBeHidden();
    await expect(lastMessageBox(electronApp)).resolves.toMatchObject({
      message: 'Delete 1 volume?',
      detail:  expect.stringContaining(testVolumeName),
    });
    await expect(volumesPage.getVolumeRow(testVolumeName)).toBeHidden({
      timeout: 20_000,
    });

    // Creating the volume is not legal in the real backend, but the mock
    // controller accepts that (and restores the mock).
    await client.createNamespacedVolume({
      namespace: APP_NAMESPACE,
      body:      {
        apiVersion: 'containers.rancherdesktop.io/v1alpha1',
        kind:       'Volume',
        metadata:   { name: testVolumeName },
      },
    });
    await volumesPage.waitForVolumeToAppear(testVolumeName);
  });

  test('should allow cancelling bulk operations', async() => {
    const volumesPage = new VolumesPage(page);

    // Disable the electron dialog box for deletion confirmation.
    await answerMessageBox(electronApp, CANCEL_DELETE);

    await Promise.all(bulkVolumeNames.map(volumeName => volumesPage.waitForVolumeToAppear(volumeName)));
    await volumesPage.deleteBulkVolumes(bulkVolumeNames);
    await expect(lastMessageBox(electronApp)).resolves.toMatchObject({
      message: `Delete ${ bulkVolumeNames.length } volumes?`,
      detail:  expect.stringMatching(
        new RegExp(bulkVolumeNames.map(name => `(?=.*\\b${ RegExp.escape(name) }\\b)`).join(''), 's'),
      ),
    });

    // Check that all volumes are still visible, and the selection state is unchanged.
    for (const volume of mockVolumes) {
      await expect(volumesPage.getVolumeRow(volume.Name)).toBeVisible();
      if (bulkVolumeNames.includes(volume.Name)) {
        await expect(volumesPage.getVolumeRow(volume.Name).locator('input[type="checkbox"]')).toBeChecked();
      } else {
        await expect(volumesPage.getVolumeRow(volume.Name).locator('input[type="checkbox"]')).not.toBeChecked();
      }
    }

    // Leave the selection unchanged, for the next test.
  });

  test('should support bulk operations', async() => {
    const volumesPage = new VolumesPage(page);
    await expect(volumesPage.errorBanner).toBeHidden();

    // The checkbox state should be maintained from the previous test.
    for (const volume of mockVolumes) {
      await expect(volumesPage.getVolumeRow(volume.Name)).toBeVisible();
      if (bulkVolumeNames.includes(volume.Name)) {
        await expect(volumesPage.getVolumeRow(volume.Name).locator('input[type="checkbox"]')).toBeChecked();
      } else {
        await expect(volumesPage.getVolumeRow(volume.Name).locator('input[type="checkbox"]')).not.toBeChecked();
      }
    }

    // Disable the electron dialog box for deletion confirmation.
    await answerMessageBox(electronApp, CONFIRM_DELETE);

    await volumesPage.clickBulkDelete();
    await expect(volumesPage.errorBanner).toBeHidden();
    await expect(lastMessageBox(electronApp)).resolves.toMatchObject({
      message: `Delete ${ bulkVolumeNames.length } volumes?`,
      detail:  expect.stringMatching(
        new RegExp(bulkVolumeNames.map(name => `(?=.*\\b${ RegExp.escape(name) }\\b)`).join(''), 's'),
      ),
    });

    for (const volumeName of bulkVolumeNames) {
      await expect(volumesPage.getVolumeRow(volumeName)).toBeHidden({
        timeout: 20_000,
      });
    }

    // Confirm the deletions stuck against the RDD API too, rather than the
    // mock backend's reconciler silently recreating them.
    const volumes = await client.listVolumeForAllNamespaces();

    for (const volumeName of bulkVolumeNames) {
      expect(volumes.items.some(volume => volume.status?.name === volumeName)).toBe(false);
    }
  });

  // FIXME: we don't have a way to propagate errors from volume deletion yet.
  // Also, the mock controller doesn't model that.
  // Mostly covered by https://github.com/rancher-sandbox/rancher-desktop-2/issues/574
  test.fixme('should display error message in banner', async() => {
    const volumesPage = new VolumesPage(page);
    const volumeName = `test-volume-in-use-${ Date.now() }`;
    const containerName = `test-container-${ Date.now() }`;

    try {
      await tool('docker', 'volume', 'create', volumeName);

      // Create container that uses volume above
      await tool(
        'docker',
        'run',
        '--detach',
        '--name',
        containerName,
        '-v',
        `${ volumeName }:/data`,
        'alpine',
        'sleep',
        'inf',
      );

      await page.reload();
      await volumesPage.waitForTableToLoad();
      await volumesPage.waitForVolumeToAppear(volumeName);

      // Try to delete volume, results in error
      await volumesPage.deleteVolume(volumeName);

      await expect(volumesPage.errorBanner).toBeVisible();

      await expect(volumesPage.errorBanner).toContainText(/volume is in use/i);

      await expect(volumesPage.getVolumeRow(volumeName)).toBeVisible();
    } finally {
      try {
        await tool('docker', 'rm', '-f', containerName);
        await tool('docker', 'volume', 'rm', volumeName);
      } catch (cleanupError) {}
    }
  });
});
