import os from 'os';

import { test, expect, _electron } from '@playwright/test';
import semver from 'semver';

import { NavPage } from './pages/nav-page';
import { PreferencesPage } from './pages/preferences';
import { KubernetesNav } from './pages/preferences/kubernetes';
import { VirtualMachineNav } from './pages/preferences/virtualMachine';
import { rdd, startRancherDesktop, teardown } from './utils/TestUtils';

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

  test.fixme('should show application page and render general tab', async() => {
    const { application } = prefPage;

    await expect(application.nav).toHaveClass('preferences-nav-item active');

    if (!os.platform().startsWith('win')) {
      await expect(application.tabEnvironment).toBeVisible();
    } else {
      await expect(application.tabEnvironment).not.toBeVisible();
    }

    await expect(application.tabGeneral).toHaveText('General');
    await expect(application.tabBehavior).toBeVisible();

    await expect(application.automaticUpdates).toBeVisible();
    await expect(application.statistics).toBeVisible();
    await expect(application.autoStart).not.toBeVisible();
    await expect(application.pathManagement).not.toBeVisible();
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

  test.fixme('should render environment tab', async() => {
    test.skip(os.platform() === 'win32', 'Environment tab not available on Windows');
    const { application } = prefPage;

    await application.tabEnvironment.click();

    await expect(application.administrativeAccess).not.toBeVisible();
    await expect(application.automaticUpdates).not.toBeVisible();
    await expect(application.statistics).not.toBeVisible();
    await expect(application.pathManagement).toBeVisible();
  });

  test.describe('Virtual Machine', () => {
    let virtualMachine: VirtualMachineNav;

    test('should navigate to virtual machine', async() => {
      virtualMachine = prefPage.virtualMachine;

      await virtualMachine.nav.click();

      await expect(virtualMachine.nav).toHaveClass('preferences-nav-item active');
      await expect(prefPage.body).toHaveAttribute('data-test-component', 'Virtual Machine');
    });

    test('should render hardware tab', async() => {
      await expect(virtualMachine.tabHardware).toBeVisible();
      await virtualMachine.tabHardware.click();

      // The max values are from `mock/hostinfo_reconciler.go`.

      await expect(virtualMachine.memory.container).toBeVisible();
      await expect(virtualMachine.memory.marks.first()).toHaveText('2');
      await expect(virtualMachine.memory.marks.last()).toHaveText('12');
      await virtualMachine.memory.marks.getByText('8').click();
      await expect(virtualMachine.memory.value).toHaveValue('8');

      await expect(virtualMachine.cpus.container).toBeVisible();
      await expect(virtualMachine.cpus.marks.first()).toHaveText('2');
      await expect(virtualMachine.cpus.marks.last()).toHaveText('32');
      await virtualMachine.cpus.marks.getByText('10').click();
      await expect(virtualMachine.cpus.value).toHaveValue('10');
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

  test.fixme('should navigate to container engine', async() => {
    const { containerEngine } = prefPage;

    await containerEngine.nav.click();

    await expect(containerEngine.nav).toHaveClass('preferences-nav-item active');
    await expect(containerEngine.containerEngine).toBeVisible();

    await expect(containerEngine.tabGeneral).toBeVisible();
    await expect(containerEngine.tabAllowedImages).toBeVisible();
  });

  test.fixme('should render allowed images tab after click on allowed images tab', async() => {
    const { containerEngine } = prefPage;

    await containerEngine.tabAllowedImages.click();

    await expect(containerEngine.allowedImages).toBeVisible();
    await expect(containerEngine.containerEngine).not.toBeVisible();
  });

  test.describe('Kubernetes', () => {
    let kubernetes: KubernetesNav;

    test('should navigate to kubernetes', async() => {
      kubernetes = prefPage.kubernetes;

      await kubernetes.nav.click();

      await expect(kubernetes.nav).toHaveClass('preferences-nav-item active');
      await expect(prefPage.body).toHaveAttribute('data-test-component', 'Kubernetes');
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

      const namespace = await rdd('ctl', 'get', 'app/app', '--output=jsonpath={.spec.namespace}');
      const versionsText = await rdd('ctl', 'get', 'configmap/k3s-versions',
        `--namespace=${ namespace }`, '--output=jsonpath={.data.versions}');
      const versionMap: Record<string, string> = JSON.parse(versionsText);
      const channelsText = await rdd('ctl', 'get', 'configmap/k3s-versions',
        `--namespace=${ namespace }`, '--output=jsonpath={.data.channels}');
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

  test.describe.fixme('Preferences State', () => {
    test.beforeAll(async() => {
      const { application } = prefPage;

      // Start this collection of tests on the environment tab
      await application.nav.click();
      if (os.platform() === 'win32') {
        await application.tabGeneral.click();
      } else {
        await application.tabEnvironment.click();
      }

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
      prefPage = new PreferencesPage(prefWindow);
    });

    test.afterEach(async() => {
      if (prefPage) {
        await prefPage.page.close();
      }
    });

    test('should render environment tab after close and reopen preferences modal', async() => {
      test.skip(os.platform() === 'win32', 'Environment tab not available on Windows');

      expect(prefPage).toBeDefined();

      const { application, containerEngine } = prefPage;

      await application.tabEnvironment.click();

      await expect(application.nav).toHaveClass('preferences-nav-item active');
      await expect(application.tabBehavior).toBeVisible();
      await expect(application.tabEnvironment).toBeVisible();
      await expect(application.administrativeAccess).not.toBeVisible();
      await expect(application.automaticUpdates).not.toBeVisible();
      await expect(application.statistics).not.toBeVisible();
      await expect(application.pathManagement).toBeVisible();

      // Move onto the container engine before starting the next test
      await containerEngine.nav.click();
      await containerEngine.tabGeneral.click();
    });

    test('should render container engine page after close and reopen preferences modal', async() => {
      expect(prefPage).toBeDefined();
      await prefPage.waitForLoad();
      const { containerEngine } = prefPage;

      if (os.platform() === 'win32') {
        // We didn't run the previous test which landed on `tabGeneral`, so run that here.
        await containerEngine.nav.click();
        await containerEngine.tabGeneral.click();
      }
      await expect(containerEngine.nav).toHaveClass('preferences-nav-item active');
      await expect(containerEngine.containerEngine).toBeVisible();

      await expect(containerEngine.tabGeneral).toBeVisible();
      await expect(containerEngine.tabAllowedImages).toBeVisible();

      // Move onto the allowed images tab before the next test
      await containerEngine.tabAllowedImages.click();
    });

    test('should render allowed image tab in container engine page after close and reopen preferences modal', async() => {
      expect(prefPage).toBeDefined();
      await prefPage.waitForLoad();
      const { containerEngine } = prefPage;

      await expect(containerEngine.nav).toHaveClass('preferences-nav-item active');
      await expect(containerEngine.allowedImages).toBeVisible();
      await expect(containerEngine.containerEngine).not.toBeVisible();
    });
  });
});
