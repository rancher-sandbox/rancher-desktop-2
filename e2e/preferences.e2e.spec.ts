import os from 'os';

import { test, expect, _electron } from '@playwright/test';
import semver from 'semver';

import { NavPage } from './pages/nav-page';
import { PreferencesPage } from './pages/preferences';
import { ContainerEngineNav } from './pages/preferences/containerEngine';
import { KubernetesNav } from './pages/preferences/kubernetes';
import { VirtualMachineNav } from './pages/preferences/virtualMachine';
import { rdd, startRancherDesktop, teardown } from './utils/TestUtils';

import { APP_NAMESPACE } from '@pkg/utils/rdd';

import type { ElectronApplication } from '@playwright/test';

let mainNav: NavPage;

test.describe.configure({ mode: 'serial' });

test.describe('Preferences Dialog', () => {
  let electronApp: ElectronApplication;
  let prefPage: PreferencesPage;

  test.beforeAll(async({ colorScheme }, testInfo) => {
    electronApp = await startRancherDesktop(testInfo);

    mainNav = new NavPage(await electronApp.firstWindow());
  });

  test.afterAll(async({ colorScheme }, testInfo) => {
    await teardown(electronApp, testInfo);
  });

  test('should finish loading', () => mainNav.waitForAppSettled());

  test('should open preferences modal', async() => {
    await mainNav.preferencesButton.click();
    const prefWindow = await electronApp.waitForEvent('window', page => /preferences/i.test(page.url()));
    expect(prefWindow).toBeDefined();
    prefPage = new PreferencesPage(prefWindow);

    await prefPage.waitForLoad();
  });

  test.describe('Application', () => {
    test('should show application page and render general tab', async() => {
      const { application } = prefPage;

      await expect(application.nav).toHaveClass('preferences-nav-item active');
      await expect(application.tabGeneral).toHaveText('General');
      await expect(application.tabEnvironment).toHaveText('Environment');
      // TODO: behavior tab visibility.
      await expect(application.tabGeneral).toHaveAttribute('aria-selected', 'true');

      await expect(application.automaticUpdates).toBeVisible();
      // TODO: telemetry visibility.
      // TODO: auto start (in)visibility.
      // TODO: path management (in)visibility.
    });

    test('should show locale selection', async() => {
      const { application } = prefPage;

      await expect(application.localeSelect).toBeEnabled();
      await expect(application.localeSelect).toHaveValue('en-us');
      try {
        await application.localeSelect.selectOption('ko');
        // Changing the selected option should change the locale
        await expect(application.tabGeneral).toHaveText(/^\p{Script=Hangul}+$/u);
      } finally {
        // Change the option back for future tests
        await application.localeSelect.selectOption('en-us');
      }
      await expect(application.tabGeneral).toHaveText('General');
    });

    test.fixme('should render behavior tab', async() => {
      const { application } = prefPage;

      await application.tabBehavior.click();

      await expect(application.autoStart).toBeVisible();
      await expect(application.background).toBeVisible();
      await expect(application.notificationIcon).toBeVisible();
      await expect(application.administrativeAccess).not.toBeVisible();
      await expect(application.pathManagement).not.toBeVisible();
    });

    test('should render environment tab', async() => {
      const { application } = prefPage;

      await application.tabEnvironment.click();
      await expect(application.tabEnvironment).toHaveAttribute('aria-selected', 'true');
      await expect(application.tabPanel).toHaveAttribute('id', 'environment');

      // Check element visibility
      await expect(application.administrativeAccess).not.toBeVisible();
      await expect(application.automaticUpdates).not.toBeVisible();
      await expect(application.statistics).not.toBeVisible();
      await expect(application.pathManagement).toBeVisible();

      // Check path management descriptions; click behaviour is handled in unit tests.
      const strings = {
        manual: 'Manual',
        front:  'Add to front',
        back:   'Append to back',
      };
      for (const [value, text] of Object.entries(strings)) {
        const element = application.pathManagement.getByTestId(`path-management-${ value }`);
        await expect(element).toBeVisible();
        await expect(element.locator('.radio-label')).toContainText(text);
        await expect(element.getByTestId('description')).toContainText('BIN_DIR');
      }
    });
  });

  test.describe('Virtual Machine', () => {
    let virtualMachine: VirtualMachineNav;

    test('should navigate to virtual machine', async() => {
      virtualMachine = prefPage.virtualMachine;

      await virtualMachine.nav.click();

      await expect(virtualMachine.nav).toHaveClass('preferences-nav-item active');
      await expect(prefPage.body).toHaveAttribute('data-test-component', 'virtual-machine');
    });

    test('should render hardware tab', async() => {
      await expect(virtualMachine.tabHardware).toBeVisible();
      await virtualMachine.tabHardware.click();

      // The max values are from `mock/hostinfo_reconciler.go`.

      await expect(prefPage.alert).toHaveText('');
      await expect(virtualMachine.memory.container).toBeVisible();
      await expect(virtualMachine.memory.marks.first()).toHaveText('2');
      await expect(virtualMachine.memory.marks.last()).toHaveText('12');
      await expect(prefPage.alert).toHaveText('');
      await virtualMachine.memory.marks.getByText('8').click();
      await expect(virtualMachine.memory.value).toHaveValue('8');
      await expect(virtualMachine.systemPreferences).toHaveAttribute('data-memory-value', '8');
      await expect(prefPage.alert).toHaveText('');
      await virtualMachine.memory.value.press('ArrowDown');
      await expect(virtualMachine.systemPreferences).toHaveAttribute('data-memory-value', '7');
      await expect(prefPage.alert).toHaveText('');
      await virtualMachine.memory.value.fill('');
      await virtualMachine.cpus.value.focus();
      await expect(prefPage.alert).toHaveText('');
      await expect(virtualMachine.systemPreferences).toHaveAttribute('data-memory-value', '7');

      await expect(virtualMachine.cpus.container).toBeVisible();
      await expect(virtualMachine.cpus.marks.first()).toHaveText('2');
      await expect(virtualMachine.cpus.marks.last()).toHaveText('32');
      await virtualMachine.cpus.marks.getByText('10').click();
      await expect(virtualMachine.cpus.value).toHaveValue('10');
      await virtualMachine.cpus.value.press('ArrowDown');
      await expect(virtualMachine.cpus.value).toHaveValue('9');
    });
  });

  test.describe('Container Engine', () => {
    let containerEngine: ContainerEngineNav;

    test('should navigate to container engine', async() => {
      containerEngine = prefPage.containerEngine;

      await containerEngine.nav.click();

      await expect(containerEngine.nav).toHaveClass('preferences-nav-item active');
      await expect(prefPage.body).toHaveAttribute('data-test-component', 'container-engine');
    });

    test('should render general tab', async() => {
      await containerEngine.tabGeneral.click();

      await expect(containerEngine.engineSelector).toBeVisible();
      await expect(containerEngine.engineSelector).toHaveAttribute('data-value', 'moby');
      await containerEngine.engineContainerd.click();
      await expect(containerEngine.engineSelector).toHaveAttribute('data-value', 'containerd');
      await containerEngine.engineMoby.click();
      await expect(containerEngine.engineSelector).toHaveAttribute('data-value', 'moby');
    });
  });

  test.fixme('should render volumes tab', async() => {
    test.skip(os.platform() === 'win32', 'Virtual Machine not available on Windows');
    const { virtualMachine } = prefPage;

    await virtualMachine.tabVolumes.click();

    await expect(virtualMachine.mountType).toBeVisible();
    await expect(virtualMachine.reverseSshFs).toBeVisible();
    await expect(virtualMachine.ninep).toBeVisible();
    await expect(virtualMachine.virtiofs).toBeVisible();

    if (os.platform() === 'darwin') {
      if (parseInt(os.release()) < 22) {
        await expect(virtualMachine.virtiofs).toBeDisabled();
      } else {
        await expect(virtualMachine.virtiofs).not.toBeDisabled();
      }
    }

    if (os.platform() === 'darwin' && parseInt(os.release()) >= 23) {
      await expect(virtualMachine.virtiofs).toBeChecked();
    } else {
      await expect(virtualMachine.reverseSshFs).toBeChecked();
    }

    await virtualMachine.ninep.click();
    await expect(virtualMachine.cacheMode).toBeVisible();
    await expect(virtualMachine.msizeInKib).toBeVisible();
    await expect(virtualMachine.protocolVersion).toBeVisible();
    await expect(virtualMachine.securityModel).toBeVisible();
  });

  test.fixme('should render emulation tab on macOS', async() => {
    test.skip(os.platform() !== 'darwin', 'Emulation tab only available on macOS');

    const { virtualMachine } = prefPage;

    await virtualMachine.tabEmulation.click();
    await expect(virtualMachine.vmType).toBeVisible();
    await expect(virtualMachine.qemu).toBeVisible();
    await expect(virtualMachine.vz).toBeVisible();

    if (parseInt(os.release()) < 22) {
      await expect(virtualMachine.vz).toBeDisabled();
    } else {
      await expect(virtualMachine.vz).not.toBeDisabled();
      await virtualMachine.vz.click({ position: { x: 10, y: 10 } });
      await expect(virtualMachine.useRosetta).toBeVisible();

      if (os.arch() === 'arm64') {
        await expect(virtualMachine.useRosetta).not.toBeDisabled();
      } else {
        await expect(virtualMachine.useRosetta).toBeDisabled();
      }
    }
  });

  test.describe('Kubernetes', () => {
    let kubernetes: KubernetesNav;

    test('should navigate to kubernetes', async() => {
      kubernetes = prefPage.kubernetes;

      await kubernetes.nav.click();

      await expect(kubernetes.nav).toHaveClass('preferences-nav-item active');
      await expect(prefPage.body).toHaveAttribute('data-test-component', 'kubernetes');
    });

    test('Kubernetes enabled checkbox should exist', async() => {
      await expect(kubernetes.kubernetesToggle).toBeVisible();
      await expect(kubernetes.kubernetesVersion).toBeVisible();

      await kubernetes.kubernetesToggle.uncheck();
      await expect(kubernetes.kubernetesToggle).not.toBeChecked();
      await expect(kubernetes.kubernetesVersion).toBeDisabled();

      await kubernetes.kubernetesToggle.check();
      await expect(kubernetes.kubernetesToggle).toBeChecked();
    });

    test('Kubernetes version dropdown should have the correct versions', async() => {
      await kubernetes.kubernetesToggle.check();
      await expect(kubernetes.kubernetesToggle).toBeChecked();
      await expect(kubernetes.kubernetesVersion).toBeVisible();
      await expect(kubernetes.kubernetesVersion).toBeEnabled();

      const options = kubernetes.kubernetesVersion.locator('option');
      // Check `stable` and `latest` separately, to avoid assuming order.
      // It is also possible that they are on the same version.
      await expect(options).toContainText(['stable']);
      await expect(options).toContainText(['latest']);

      const versionsText = await rdd('ctl', 'get', 'configmap/k3s-versions',
        `--namespace=${ APP_NAMESPACE }`, '--output=jsonpath={.data.versions}');
      const versionMap: Record<string, string> = JSON.parse(versionsText);
      const channelsText = await rdd('ctl', 'get', 'configmap/k3s-versions',
        `--namespace=${ APP_NAMESPACE }`, '--output=jsonpath={.data.channels}');
      const channelsMap: Record<string, string> = JSON.parse(channelsText);
      const versions = Object.keys(versionMap).sort((a, b) => {
        return semver.compare(b, a); // Newest first.
      });
      const recommendedVersions = versions.map(version => {
        const channels = Object.entries(channelsMap)
          .filter(([_, v]) => v === version)
          .map(([k]) => k);
        return [version, channels.sort()] as const;
      }).filter(([_, channels]) => channels.length > 0);
      const recommendedLabels = recommendedVersions.map(([version, channels]) => {
        const filteredChannels = channels.filter(c => !/^v?\d+\.\d+/.test(c));
        if (filteredChannels.length === 0) {
          return version;
        }
        return `${ version } (${ filteredChannels.join(', ') })`;
      });
      const otherVersions = versions.filter(version => !recommendedVersions.some(([v]) => v === version));
      await expect(options).toContainText([...recommendedLabels, ...otherVersions]);
    });

    test.fixme('Kubernetes options should exist', async() => {
      await expect(kubernetes.kubernetesOptions).toBeVisible();
    });
  });

  test.fixme('should navigate to WSL and render integrations tab', async() => {
    test.skip(os.platform() !== 'win32', 'WSL nav item not available on macOS & Linux');
    const { wsl } = prefPage;

    await wsl.nav.click();

    await expect(wsl.nav).toHaveClass('preferences-nav-item active');

    await wsl.tabIntegrations.click();
    await expect(wsl.wslIntegrations).toBeVisible();
  });

  test.fixme('should not render WSL nav item on macOS and Linux', async() => {
    test.skip(os.platform() === 'win32', 'WSL nav item is only available on Windows');
    const { wsl } = prefPage;

    await expect(wsl.nav).not.toBeVisible();
  });

  test.describe('Preferences State', () => {
    test.beforeAll(async() => {
      const { kubernetes } = prefPage;

      // Start this collection of tests on the Kubernetes tab
      await kubernetes.nav.click();

      // This collection of tests is about making sure that we persist state
      // in the preferences window, so we close the preferences window before
      // beginning this test collection.
      if (prefPage) {
        await prefPage.page.close();
      }
    });

    test.beforeEach(async() => {
      await mainNav.preferencesButton.click();
      const prefWindow = await electronApp.waitForEvent('window', page => /preferences/i.test(page.url()));
      expect(prefWindow).toBeDefined();
      prefPage = new PreferencesPage(prefWindow);
    });

    test.afterEach(async() => {
      if (prefPage) {
        await prefPage.page.close();
      }
    });

    test('should render Kubernetes tab after close and reopen preferences modal', async() => {
      expect(prefPage).toBeDefined();

      const { kubernetes } = prefPage;

      await expect(kubernetes.nav).toHaveClass('preferences-nav-item active');
      await expect(prefPage.body).toHaveAttribute('data-test-component', 'kubernetes');

      await expect(kubernetes.kubernetesToggle).toBeVisible();
    });

    // Once we have pref pages with multiple sub-tabs, we can test them here.
  });
});
