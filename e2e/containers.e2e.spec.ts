import { randomBytes } from 'node:crypto';

import { expect, test, ElectronApplication, Page } from '@playwright/test';

import { ContainerInspectPage } from './pages/container-inspect-page';
import { ContainerLogsPage } from './pages/container-logs-page';
import { ContainerShellPage } from './pages/container-shell-page';
import { ContainerStatsPage } from './pages/container-stats-page';
import { ContainersPage } from './pages/containers-page';
import { NavPage } from './pages/nav-page';
import { getRDDConfig, startSlowerDesktop, teardown, tool } from './utils/TestUtils';

import * as RDDClient from '@rdd-client';

let page: Page;

test.describe.configure({ mode: 'serial' });

test.describe('Containers', () => {
  let electronApp: ElectronApplication;
  let containersPage: ContainersPage;
  let client: RDDClient.ContainersRancherdesktopIoV1alpha1Api;
  let namespace: string;
  let rddConfigCleanup = () => {};

  test.beforeAll(async({ colorScheme }, testInfo) => {
    [electronApp, page] = await startSlowerDesktop(testInfo);
    const [rddConfig, cleanup] = await getRDDConfig();
    rddConfigCleanup = cleanup;
    client = rddConfig.makeApiClient(RDDClient.ContainersRancherdesktopIoV1alpha1Api);

    const navPage = new NavPage(page);
    await navPage.waitForAppSettled();

    const apps = await rddConfig.makeApiClient(RDDClient.AppRancherdesktopIoV1alpha1Api)
      .listApp();
    namespace = apps.items[0].spec!.namespace!;
  });

  test.afterAll(async({ colorScheme }, testInfo) => {
    await teardown(electronApp, testInfo);
    rddConfigCleanup();
  });

  /**
   * Return the first container that has the given mock log writer label
   */
  async function getContainerByLogsWriter(writer: string) {
    const containers = await client.listContainerForAllNamespaces();
    const container = containers.items.find(d => {
      return (d.status?.labels?.['io.rancherdesktop.mock.logs.writer'] ?? '') === writer;
    });
    expect(container, `expected container with logs writer ${ writer }`).toBeDefined();
    if (!container) {
      // This only exists to let TypeScript know that container is defined after the expect() above.
      throw new Error(`No container found with logs writer ${ writer }`);
    }
    return container;
  }

  /**
   * Return some unspecified container.
   */
  async function getAnyContainer() {
    const containers = await client.listContainerForAllNamespaces();
    expect(containers.items.length).toBeGreaterThan(0);
    return containers.items[0];
  }

  test('should navigate to containers page', async() => {
    const navPage = new NavPage(page);
    containersPage = await navPage.navigateTo('Containers');

    await expect(navPage.mainTitle).toHaveText('Containers');
    await containersPage.waitForTableToLoad();
  });

  test('should display test container', async() => {
    const testContainer = await getAnyContainer();
    const testContainerId = testContainer.metadata!.name!;
    const testContainerName = testContainer.status!.name;

    const row = await containersPage.waitForContainerToAppear(testContainerId);
    await expect(row.locator('.container-name-link')).toHaveText(testContainerName);
    await containersPage.viewContainerInfo(testContainerId);

    await page.waitForURL(`**/containers/info/${ testContainerId }**`, {
      timeout: 10_000,
    });
  });

  test('should display container logs page', async() => {
    await page.getByTestId('tab-logs').click();

    const containerLogsPage = new ContainerLogsPage(page);

    await expect(containerLogsPage.containerInfo).toBeVisible();

    await expect(containerLogsPage.terminal).toBeVisible();
    await expect(containerLogsPage.loadingIndicator).not.toBeVisible();
  });

  test('should keep the terminal fitted after zooming out', async() => {
    const containerLogsPage = new ContainerLogsPage(page);

    await containerLogsPage.waitForLogsToLoad();
    expect(await containerLogsPage.getTerminalRows()).toBeGreaterThan(5);

    // Zooming out must not collapse the terminal to a single row (#10543).
    await electronApp.evaluate(({ BrowserWindow }, zoomLevel) => {
      BrowserWindow.getAllWindows()[0]?.webContents.setZoomLevel(zoomLevel);
    }, -2);

    try {
      await expect.poll(() => containerLogsPage.getTerminalRows()).toBeGreaterThan(5);
    } finally {
      await electronApp.evaluate(({ BrowserWindow }) => {
        BrowserWindow.getAllWindows()[0]?.webContents.setZoomLevel(0);
      });
    }
  });

  test('should show container information', async() => {
    const testContainer = await getAnyContainer();
    const testContainerName = testContainer.status!.name;
    const containerLogsPage = new ContainerLogsPage(page);

    await expect(containerLogsPage.containerInfo).toBeVisible();

    await expect(containerLogsPage.containerName).toContainText(testContainerName);
    await expect(containerLogsPage.containerState).not.toBeEmpty();
  });

  test('should display logs content', async() => {
    const container = await getContainerByLogsWriter('labels');
    const containerId = container.metadata!.name!;

    // Navigate to a container using the "labels" mock log writer...
    containersPage = await new NavPage(page).navigateTo('Containers');
    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(containerId);
    await containersPage.viewContainerInfo(containerId);
    await page.getByTestId('tab-logs').click();

    const containerLogsPage = new ContainerLogsPage(page);
    await containerLogsPage.waitForLogsToLoad();

    // At this point, the logs just contain the labels.
    expect(container.status?.labels).toHaveProperty(['io.rancherdesktop.mock.logs.writer'], 'labels');
    expect(container.status?.labels).toHaveProperty(['org.opencontainers.image.vendor'], 'openSUSE Project');
    await expect(containerLogsPage.terminal).toContainText('org.opencontainers.image.vendor');
  });

  test('should support log search', async() => {
    const container = await getContainerByLogsWriter('ten');
    expect(container).toHaveProperty('metadata.name', expect.any(String));
    const containerId = container.metadata!.name!;

    // Navigate to a container using the "count" mock log writer...
    containersPage = await new NavPage(page).navigateTo('Containers');
    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(containerId);
    await containersPage.viewContainerInfo(containerId);
    await page.getByTestId('tab-logs').click();

    const containerLogsPage = new ContainerLogsPage(page);

    // Wait for all the relevant text to show up before proceeding.
    await expect(containerLogsPage.terminal).toContainText('[Line 2]');
    await expect(containerLogsPage.searchInput).toBeVisible();

    const searchTerm = 'Line';
    await containerLogsPage.searchLogs(searchTerm);

    const searchHighlight = page.locator('span.xterm-decoration-top');
    await expect(searchHighlight).toBeVisible();

    const highlightedRow = containerLogsPage.terminal.locator(
      '.xterm-rows div',
      {
        has: page.locator('.xterm-decoration-top'),
      },
    );

    await expect(highlightedRow).toContainText('[Line 1]');

    await containerLogsPage.searchNextButton.click();

    await expect(searchHighlight).toBeVisible();
    await expect(highlightedRow).toContainText('[Line 2]');

    await containerLogsPage.searchPrevButton.click();

    await expect(searchHighlight).toBeVisible();
    await expect(highlightedRow).toContainText('[Line 1]');

    await containerLogsPage.searchClearButton.click();
    await expect(containerLogsPage.searchInput).toBeEmpty();

    await containerLogsPage.terminal.click();

    await expect(searchHighlight).not.toBeVisible();
  });

  test('should handle terminal scrolling', async() => {
    const container = await getContainerByLogsWriter('hundred');
    expect(container).toHaveProperty('metadata.name', expect.any(String));
    const scrollTestContainerId = container.metadata!.name!;

    const navPage = new NavPage(page);
    const containersPage = await navPage.navigateTo('Containers');

    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(scrollTestContainerId);
    await containersPage.viewContainerInfo(scrollTestContainerId);

    await page.waitForURL(`**/containers/info/${ scrollTestContainerId }**`, {
      timeout: 10_000,
    });

    await page.getByTestId('tab-logs').click();

    const containerLogsPage = new ContainerLogsPage(page);
    await containerLogsPage.waitForLogsToLoad();

    const terminalRows = containerLogsPage.terminal.locator('.xterm-rows');
    const lastLine = terminalRows.getByText('[Line 100]', { exact: false });
    const firstLine = terminalRows.getByText('[Line 1]', { exact: false });

    await expect(lastLine).toBeVisible();
    await expect(firstLine).not.toBeVisible();

    await containerLogsPage.scrollToTop();

    await expect(firstLine).toBeVisible();
    await expect(lastLine).not.toBeVisible();

    await containerLogsPage.scrollToBottom();

    await expect(lastLine).toBeVisible();
    await expect(firstLine).not.toBeVisible();
  });

  test('should output logs if container not exited', async() => {
    const container = await getContainerByLogsWriter('loop');
    expect(container).toHaveProperty('metadata.name', expect.any(String));
    const loopContainerId = container.metadata!.name!;

    const navPage = new NavPage(page);
    const containersPage = await navPage.navigateTo('Containers');

    await page.reload();
    await containersPage.waitForTableToLoad();

    await containersPage.waitForContainerToAppear(loopContainerId);
    await containersPage.viewContainerInfo(loopContainerId);

    await page.waitForURL(`**/containers/info/${ loopContainerId }**`, {
      timeout: 10000,
    });

    await page.getByTestId('tab-logs').click();

    const containerLogsPage = new ContainerLogsPage(page);
    await containerLogsPage.waitForLogsToLoad();

    const locator = containerLogsPage.terminal.locator('.xterm-screen');
    await expect(locator.getByText(/\[Line \d+\]/).nth(1)).toBeVisible();

    await expect(containerLogsPage.terminal).toContainText('Line ');
  });

  test('should auto-refresh containers list', async() => {
    const containersPage = new ContainersPage(page);
    const containerId = randomBytes(32).toString('hex');
    let container: RDDClient.IoRancherdesktopContainersV1alpha1Container | undefined;

    try {
      const navPage = new NavPage(page);
      await navPage.navigateTo('Containers');
      await containersPage.waitForTableToLoad();
      const containers = await client.listNamespacedContainer({ namespace });
      await expect(containersPage.containers).toHaveCount(containers.items.length);
      await expect(containersPage.getContainerRow(containerId)).toBeHidden();

      container = await client.createNamespacedContainer({
        namespace,
        body:      {
          apiVersion: 'containers.rancherdesktop.io/v1alpha1',
          kind:       'Container',
          metadata:   {
            name: containerId,
            namespace,
          },
          spec:       {
            image: 'alpine',
            args:  ['sleep', 'infinity'],
          },
        },
      });
      expect(container).toHaveProperty('metadata.name', containerId);
      container = await client.patchNamespacedContainerStatus({
        name:      containerId,
        namespace,
        body:      {
          status: {
            name:      'test_container',
            namespace: 'k8s.io',
            image:     `sha256:${ randomBytes(32).toString('hex') }`,
            path:      '/bin/false',
          },
        },
      }, RDDClient.setHeaderOptions(
        'Content-Type', RDDClient.PatchStrategy.MergePatch));

      await expect(client.listNamespacedContainer({ namespace })).resolves.toEqual(
        expect.objectContaining({
          items: expect.arrayContaining([
            expect.objectContaining({
              metadata: expect.objectContaining({
                name: containerId,
              }),
            }),
          ]),
        }),
      );
      await expect(containersPage.getContainerRow(containerId)).toBeVisible();

      await client.deleteNamespacedContainer({
        namespace,
        name: containerId,
      });

      await expect(containersPage.getContainerRow(containerId)).toBeHidden();
    } finally {
      if (container) {
        try {
          await client.deleteNamespacedContainer({
            namespace,
            name: containerId,
          });
        } catch {}
      }
    }
  });
});

test.describe.fixme('Container Shell Tab', () => {
  let electronApp: ElectronApplication;
  let shellContainerId: string;
  let unsupportedContainerId: string;

  test.beforeAll(async({ colorScheme }, testInfo) => {
    [electronApp, page] = await startSlowerDesktop(testInfo);

    const navPage = new NavPage(page);
    await navPage.waitForAppSettled();

    // Start a long-running container for the shell tests.
    // Ubuntu is used because the base Alpine image does not include `script`
    // (util-linux), which is required for the interactive shell session.
    const output = await tool('docker', 'run', '--detach', 'ubuntu', 'sleep', 'infinity');
    shellContainerId = output.trim();

    // Alpine container for the "unsupported" test — Alpine's base image has no
    // `script` command, so the shell tab should show the unsupported banner.
    const alpineOutput = await tool('docker', 'run', '--detach', 'alpine', 'sleep', 'infinity');
    unsupportedContainerId = alpineOutput.trim();
  });

  test.afterAll(async({ colorScheme }, testInfo) => {
    if (shellContainerId) {
      try {
        await tool('docker', 'rm', '-f', shellContainerId);
      } catch {}
    }
    if (unsupportedContainerId) {
      try {
        await tool('docker', 'rm', '-f', unsupportedContainerId);
      } catch {}
    }
    await teardown(electronApp, testInfo);
  });

  async function navigateToShellTab() {
    const navPage = new NavPage(page);
    await navPage.navigateTo('Containers');
    const containersPage = new ContainersPage(page);
    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(shellContainerId);
    await containersPage.clickContainerAction(shellContainerId, 'info');
    await page.waitForURL(`**/containers/info/${ shellContainerId }**`, { timeout: 10_000 });
    const shellPage = new ContainerShellPage(page);
    await shellPage.clickTab();
    await shellPage.waitForTerminal();
    await shellPage.waitForShellReady();

    return shellPage;
  }

  test('should show the Shell tab on a running container', async() => {
    const shellPage = await navigateToShellTab();
    await expect(shellPage.tab).toBeVisible();
    await expect(shellPage.terminal).toBeVisible();
    await expect(shellPage.notRunningBanner).not.toBeVisible();
  });

  test('should execute a command and display its output', async() => {
    const shellPage = new ContainerShellPage(page);
    // Unique marker avoids false positives from any earlier terminal history.
    const marker = `RDTEST_${ Date.now() }`;
    await shellPage.runCommand(`echo ${ marker }`);
    await shellPage.waitForOutput(marker);
  });

  test('should preserve the session when switching between Logs and Shell tabs', async() => {
    const shellPage = new ContainerShellPage(page);
    const logsTab = page.getByTestId('tab-logs');
    // A unique marker is required: we must distinguish "this exact output is
    // still in the buffer" from "the shell printed something similar".
    const marker = `RDTEST_PERSIST_${ Date.now() }`;

    await shellPage.runCommand(`echo ${ marker }`);
    await shellPage.waitForOutput(marker);

    // Switch to Logs and back.
    await logsTab.click();
    await shellPage.clickTab();

    // History must still be visible — session was preserved.
    await shellPage.waitForOutput(marker);
  });

  test('should show the unsupported banner for containers without script', async() => {
    // Navigate to the Alpine container — it has no `script`, so the backend
    // will send container-exec/unsupported instead of starting a session.
    const navPage = new NavPage(page);
    await navPage.navigateTo('Containers');
    const containersPage = new ContainersPage(page);
    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(unsupportedContainerId);
    await containersPage.clickContainerAction(unsupportedContainerId, 'info');
    await page.waitForURL(`**/containers/info/${ unsupportedContainerId }**`, { timeout: 10_000 });

    const shellPage = new ContainerShellPage(page);
    await shellPage.clickTab();

    // The unsupported banner must appear and the terminal must not be rendered.
    await expect(shellPage.unsupportedBanner).toBeVisible({ timeout: 15_000 });
    await expect(shellPage.terminal).not.toBeVisible();
  });
});

test.describe.fixme('Container Info Tab', () => {
  let electronApp: ElectronApplication;
  let infoContainerId: string;

  test.beforeAll(async({ colorScheme }, testInfo) => {
    [electronApp, page] = await startSlowerDesktop(testInfo);

    const navPage = new NavPage(page);
    await navPage.waitForAppSettled();

    // Run a named alpine container with a known env var so we can assert on it.
    const output = await tool(
      'docker', 'run', '--detach',
      '--env', 'RDTEST_VAR=hello',
      '--name', `rdtest-info-${ Date.now() }`,
      'alpine', 'sleep', 'infinity',
    );
    infoContainerId = output.trim();

    const containersPage = await navPage.navigateTo('Containers');
    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(infoContainerId);
    await containersPage.clickContainerAction(infoContainerId, 'info');
    await page.waitForURL(`**/containers/info/${ infoContainerId }**`, { timeout: 10_000 });
  });

  test.afterAll(async({ colorScheme }, testInfo) => {
    if (infoContainerId) {
      try {
        await tool('docker', 'rm', '-f', infoContainerId);
      } catch {}
    }
    await teardown(electronApp, testInfo);
  });

  test('Info tab is the default and active tab', async() => {
    const infoPage = new ContainerInspectPage(page);

    await expect(infoPage.tab).toHaveClass(/\bactive\b/);
  });

  test('summary table shows key container fields', async() => {
    const infoPage = new ContainerInspectPage(page);

    await infoPage.waitForData();

    // ID should be exactly 12 hex chars.
    const id = await infoPage.getSummaryValue('info-row-id');

    expect(id).toMatch(/^[a-f0-9]{12}$/);

    // Name should not start with '/'.
    const name = await infoPage.getSummaryValue('info-row-name');

    expect(name).not.toMatch(/^\//);

    // Image row should be non-empty.
    const image = await infoPage.getSummaryValue('info-row-image');

    expect(image).not.toBe('');
  });

  test('summary table shows IP address', async() => {
    const infoPage = new ContainerInspectPage(page);

    // IP row is always rendered; value is either an IP or '—'.
    const ip = await infoPage.getSummaryValue('info-row-ip');

    expect(ip).not.toBe('');
  });

  test('ports section is always visible', async() => {
    const infoPage = new ContainerInspectPage(page);

    // The alpine container has no published ports, but the section must still render.
    await expect(infoPage.portsSection).toBeVisible();
    await infoPage.portsSection.locator('summary').click();
    await expect(infoPage.portsSection).toHaveAttribute('open');
    await expect(infoPage.portsSection).toContainText('None');
  });

  test('mounts section can be expanded', async() => {
    const infoPage = new ContainerInspectPage(page);

    await infoPage.mountsSection.locator('summary').click();
    // After opening the <details>, the section body should appear.
    await expect(infoPage.mountsSection).toHaveAttribute('open');
  });

  test('switching to Logs tab and back preserves Info data', async() => {
    const infoPage = new ContainerInspectPage(page);

    await page.getByTestId('tab-logs').click();
    await infoPage.clickTab();
    await infoPage.waitForData();

    // Summary table must still be populated after round-trip.
    const id = await infoPage.getSummaryValue('info-row-id');

    expect(id).toMatch(/^[a-f0-9]{12}$/);
  });
});

test.describe.fixme('Container Stats Tab', () => {
  let electronApp: ElectronApplication;
  let runningContainerId: string;
  let stoppedContainerId: string;

  test.beforeAll(async({ colorScheme }, testInfo) => {
    [electronApp, page] = await startSlowerDesktop(testInfo);

    const navPage = new NavPage(page);
    await navPage.waitForAppSettled();

    // Long-running container for the "running" tests.
    const runOutput = await tool('docker', 'run', '--detach', 'alpine', 'sleep', 'infinity');

    runningContainerId = runOutput.trim();

    // Stopped container (exits immediately).
    const stopOutput = await tool('docker', 'run', '--detach', 'alpine', 'echo', 'done');

    stoppedContainerId = stopOutput.trim();
    // Give it a moment to exit.
    await new Promise(resolve => setTimeout(resolve, 2_000));
  });

  test.afterAll(async({ colorScheme }, testInfo) => {
    for (const id of [runningContainerId, stoppedContainerId]) {
      if (id) {
        try {
          await tool('docker', 'rm', '-f', id);
        } catch {}
      }
    }
    await teardown(electronApp, testInfo);
  });

  async function navigateToStatsTab(containerId: string): Promise<ContainerStatsPage> {
    const navPage = new NavPage(page);
    await navPage.navigateTo('Containers');
    const containersPage = new ContainersPage(page);
    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(containerId);
    await containersPage.clickContainerAction(containerId, 'info');
    await page.waitForURL(`**/containers/info/${ containerId }**`, { timeout: 10_000 });
    const statsPage = new ContainerStatsPage(page);
    await statsPage.clickTab();

    return statsPage;
  }

  test('Stats tab appears between Info and Logs', async() => {
    const navPage = new NavPage(page);
    await navPage.navigateTo('Containers');
    const containersPage = new ContainersPage(page);
    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(runningContainerId);
    await containersPage.clickContainerAction(runningContainerId, 'info');
    await page.waitForURL(`**/containers/info/${ runningContainerId }**`, { timeout: 10_000 });

    const tabs = page.locator('.tabs li.tab');
    const tabTexts = await tabs.allTextContents();
    const names = tabTexts.map((t: string) => t.trim());

    const infoIdx = names.findIndex((n: string) => n === 'Info');
    const statsIdx = names.findIndex((n: string) => n === 'Stats');
    const logsIdx = names.findIndex((n: string) => n === 'Logs');

    expect(infoIdx).toBeGreaterThanOrEqual(0);
    expect(statsIdx).toBeGreaterThan(infoIdx);
    expect(logsIdx).toBeGreaterThan(statsIdx);
  });

  test('charts render on a running container', async() => {
    const statsPage = await navigateToStatsTab(runningContainerId);
    await statsPage.waitForCharts();
  });

  test('process table shows at least one row', async() => {
    const statsPage = await navigateToStatsTab(runningContainerId);
    await expect(statsPage.processTable.locator('tbody tr').first()).toBeVisible({ timeout: 15_000 });
  });

  test('refresh rate select has the correct options', async() => {
    const statsPage = await navigateToStatsTab(runningContainerId);

    await expect(statsPage.refreshSelect).toBeVisible({ timeout: 15_000 });
    const options = await statsPage.refreshSelect.locator('option').allTextContents();

    expect(options).toEqual(['1 s', '5 s', '10 s', '20 s', '30 s', '1 min']);
  });

  test('stopped container shows not-running banner', async() => {
    const statsPage = await navigateToStatsTab(stoppedContainerId);

    await expect(statsPage.notRunningBanner).toBeVisible({ timeout: 10_000 });
    await expect(statsPage.cpuChart).not.toBeVisible();
  });
});

test.describe.fixme('Container Compose Group Actions', () => {
  let electronApp: ElectronApplication;
  let composeContainerId1: string;
  let composeContainerId2: string;
  const composeProjectName = `test-compose-project-${ Date.now() }`;

  test.beforeAll(async({ colorScheme }, testInfo) => {
    [electronApp, page] = await startSlowerDesktop(testInfo);

    const navPage = new NavPage(page);
    await navPage.waitForAppSettled();

    // A shared label fakes a compose project, so these tests need no
    // docker-compose CLI plugin.
    const output1 = await tool(
      'docker', 'run', '--detach',
      '--label', `com.docker.compose.project=${ composeProjectName }`,
      '--name', `${ composeProjectName }-app`,
      'alpine', 'sleep', 'infinity',
    );

    composeContainerId1 = output1.trim();

    const output2 = await tool(
      'docker', 'run', '--detach',
      '--label', `com.docker.compose.project=${ composeProjectName }`,
      '--name', `${ composeProjectName }-db`,
      'alpine', 'sleep', 'infinity',
    );

    composeContainerId2 = output2.trim();
  });

  test.afterAll(async({ colorScheme }, testInfo) => {
    for (const id of [composeContainerId1, composeContainerId2]) {
      if (id) {
        try {
          await tool('docker', 'rm', '-f', id);
        } catch {}
      }
    }
    await teardown(electronApp, testInfo);
  });

  test('compose project group shows a select-all checkbox', async() => {
    const navPage = new NavPage(page);
    const containersPage = await navPage.navigateTo('Containers');

    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(composeContainerId1);
    await containersPage.waitForContainerToAppear(composeContainerId2);
    await containersPage.waitForGroupToAppear(composeProjectName);

    await expect(containersPage.getGroupCheckbox(composeProjectName)).toBeVisible();
  });

  test('group checkbox selects every container in the project for bulk actions', async() => {
    const navPage = new NavPage(page);
    const containersPage = await navPage.navigateTo('Containers');

    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(composeContainerId1);
    await containersPage.waitForContainerToAppear(composeContainerId2);
    await containersPage.waitForGroupToAppear(composeProjectName);
    await containersPage.selectGroup(composeProjectName);

    // The row checkboxes do not set the native `checked` DOM property, so
    // assert on the table's own selection count label.
    await expect(containersPage.getSelectionCount(2)).toBeVisible();

    await containersPage.clickBulkStop();

    for (const id of [composeContainerId1, composeContainerId2]) {
      await expect(containersPage.getContainerRow(id).getByText('exited')).toBeVisible({ timeout: 15_000 });
    }
  });

  test('group checkbox plus the regular delete button removes every container in the project', async() => {
    const navPage = new NavPage(page);
    const containersPage = await navPage.navigateTo('Containers');

    await containersPage.waitForTableToLoad();
    await containersPage.waitForContainerToAppear(composeContainerId1);
    await containersPage.waitForContainerToAppear(composeContainerId2);
    await containersPage.waitForGroupToAppear(composeProjectName);
    await containersPage.selectGroup(composeProjectName);
    await containersPage.clickBulkDelete();

    for (const id of [composeContainerId1, composeContainerId2]) {
      await expect(containersPage.getContainerRow(id)).not.toBeVisible({ timeout: 15_000 });
    }
  });
});
