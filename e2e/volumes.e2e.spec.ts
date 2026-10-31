import { ElectronApplication, Page, expect, test } from '@playwright/test';

import { NavPage } from './pages/nav-page';
import { VolumesPage } from './pages/volumes-page';
import { getRDDConfig, startSlowerDesktop, teardown, tool } from './utils/TestUtils';

import mockVolumes from '@/rdd/pkg/controllers/mock/testdata/volumes.json' with { type: 'json' };
import * as RDDClient from '@rdd-client';

let page: Page;

test.describe.configure({ mode: 'serial' });

test.describe('Volumes Tests', () => {
  let electronApp: ElectronApplication;
  let client: RDDClient.ContainersRancherdesktopIoV1alpha1Api;
  let rddConfigCleanup = () => {};

  // The mock backend has no dynamic create-volume flow; it always mirrors
  // whatever is in its static fixture.  We use the first for general testing,
  // and the rest for testing bulk operations.
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

  test('should auto-refresh volumes list', async() => {
    const volumesPage = new VolumesPage(page);

    await volumesPage.waitForVolumeToAppear(testVolumeName);
    await expect(volumesPage.errorBanner).toBeHidden();
    await volumesPage.deleteVolume(testVolumeName);
    await expect(volumesPage.errorBanner).toBeHidden();
    await expect(volumesPage.getVolumeRow(testVolumeName)).toBeHidden({
      timeout: 20_000,
    });

    // Creating the volume is not legal in the real backend, but the mock
    // controller accepts that (and restores the mock).
    await client.createNamespacedVolume({
      namespace: 'rancher-desktop',
      body:      {
        apiVersion: 'containers.rancherdesktop.io/v1alpha1',
        kind:       'Volume',
        metadata:   { name: testVolumeName },
      },
    });
    await volumesPage.waitForVolumeToAppear(testVolumeName);
  });

  test('should support bulk operations', async() => {
    const volumesPage = new VolumesPage(page);

    // The mock backend can't fabricate new volumes on demand, so this
    // exercises bulk selection/deletion against several of the fixture's
    // other pre-existing static volumes.
    await Promise.all(bulkVolumeNames.map(volumeName => volumesPage.waitForVolumeToAppear(volumeName)));
    await expect(volumesPage.errorBanner).toBeHidden();

    await volumesPage.deleteBulkVolumes(bulkVolumeNames);
    await expect(volumesPage.errorBanner).toBeHidden();

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

  // FIXME: we don't have a way to propogate errors from volume deletion yet.
  // Also, the mock controller doesn't model that.
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
