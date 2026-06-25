/**
 * This script builds the distributable packages. It assumes that we have _just_
 * built the JavaScript parts.
 */

'use strict';

import fs from 'fs';
import * as path from 'path';

import { flipFuses, FuseV1Options, FuseVersion } from '@electron/fuses';
import { DOMParser, XMLSerializer } from '@xmldom/xmldom';
import { executeAppBuilder, log } from 'builder-util';
import {
  AfterPackContext, Arch, build, CliOptions, Configuration, LinuxTargetSpecificOptions,
} from 'electron-builder';
import _ from 'lodash';
import * as plist from 'plist';
import yaml from 'yaml';

import buildUtils from './lib/build-utils';
import buildInstaller, { buildCustomAction } from './lib/installer-win32';

import { spawnFile } from '@pkg/utils/childProcess';
import { ReadWrite } from '@pkg/utils/typeUtils';

class Builder {
  /**
   * Flip the Electron fuses so that the app can't be used as a node runtime.
   * @see https://www.electronjs.org/docs/latest/tutorial/fuses
   */
  protected async flipFuses(context: AfterPackContext) {
    const extension = {
      darwin: '.app',
      win32:  '.exe',
    }[context.electronPlatformName] ?? '';
    const exeName = `${ context.packager.appInfo.productFilename }${ extension }`;
    const exePath = path.join(context.appOutDir, exeName);
    const resetAdHocDarwinSignature = context.arch === Arch.arm64;
    const integrityEnabled = context.electronPlatformName === 'darwin';

    await flipFuses(
      exePath,
      {
        version:                                               FuseVersion.V1,
        resetAdHocDarwinSignature,
        [FuseV1Options.RunAsNode]:                             false,
        [FuseV1Options.EnableCookieEncryption]:                false,
        [FuseV1Options.EnableNodeOptionsEnvironmentVariable]:  false,
        [FuseV1Options.EnableNodeCliInspectArguments]:         false,
        [FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: integrityEnabled,
        [FuseV1Options.OnlyLoadAppFromAsar]:                   true,
      },
    );
  }

  /**
   * Manually write out the Linux .desktop application shortcut definition; this
   * is needed as by default this only happens for snap/fpm/etc., but not zip
   * files.
   */
  protected async writeLinuxDesktopFile(context: AfterPackContext) {
    const { LinuxPackager } = await import('app-builder-lib/out/linuxPackager');
    const { LinuxTargetHelper } = await import('app-builder-lib/out/targets/LinuxTargetHelper');
    const config = context.packager.config.linux;

    if (!(context.packager instanceof LinuxPackager) || !config) {
      return;
    }

    const options: LinuxTargetSpecificOptions = {
      ...context.packager.platformSpecificBuildOptions,
      compression: undefined,
    };
    const helper = new LinuxTargetHelper(context.packager);
    const leaf = `${ context.packager.executableName }.desktop`;
    const destination = path.join(context.appOutDir, `resources/linux/${ leaf }`);

    await helper.writeDesktopEntry(options, context.packager.executableName, destination);
  }

  /**
   * Edit the application's `Info.plist` file to remove the UsageDescription
   * keys; there is no reason for the application to get any of those permissions.
   */
  protected async removeMacUsageDescriptions(context: AfterPackContext) {
    const { MacPackager } = await import('app-builder-lib/out/macPackager');
    const { packager } = context;
    const config = packager.config.mac;

    if (!(packager instanceof MacPackager) || !config) {
      return;
    }

    const { productFilename } = packager.appInfo;
    const appPath = path.join(context.appOutDir, `${ productFilename }.app`);
    const plistPath = path.join(appPath, 'Contents', 'Info.plist');
    const plistContents = await fs.promises.readFile(plistPath, 'utf-8');
    const plistData = plist.parse(plistContents);

    if (typeof plistData !== 'object' || !('CFBundleName' in plistData)) {
      return;
    }
    const plistCopy: Record<string, plist.PlistValue> = structuredClone(plistData);

    for (const key in plistData) {
      if (/^NS.*UsageDescription$/.test(key)) {
        delete plistCopy[key];
      }
    }
    await fs.promises.writeFile(plistPath, plist.build(plistCopy), 'utf-8');

    // Because we modified the Info.plist, we need to re-sign the app.  This is
    // just using ad-hoc signing.  Note that this will fail on x86_64, so ignore
    // it there.
    if (context.arch !== Arch.x64) {
      await spawnFile('codesign', ['--sign', '-', '--force', '--verbose', appPath], { stdio: 'inherit' });
    }
  }

  protected async afterPack(context: AfterPackContext) {
    await this.flipFuses(context);
    await this.writeLinuxDesktopFile(context);
    await this.removeMacUsageDescriptions(context);
  }

  async package(): Promise<CliOptions> {
    log.info('Packaging...');

    // Build the electron builder configuration to include the version data
    const config: ReadWrite<Configuration> = yaml.parse(await fs.promises.readFile('packaging/electron-builder.yml', 'utf-8'));
    const configPath = path.join(buildUtils.distDir, 'electron-builder.yaml');
    const version = await buildUtils.version;

    const distDir = path.join(process.cwd(), 'dist');
    const electronPlatform = ({
      darwin: 'mac',
      win32:  'win',
      linux:  'linux',
    } as const)[process.platform as string];

    if (!electronPlatform) {
      throw new Error(`Packaging for ${ process.platform } is not supported`);
    }

    _.set(config, 'productName', buildUtils.packageMeta.productName);
    _.set(config, 'extraMetadata.version', version);

    switch (electronPlatform) {
    case 'linux':
      await this.createLinuxResources(config);
      break;
    case 'mac':
      await this.createMacResources(distDir);
      break;
    case 'win':
      await this.createWindowsResources(distDir);
      break;
    }

    // When there are files (e.g., extraFiles or extraResources) specified at both
    // the top-level and platform-specific levels, we need to combine them
    // and place the combined list at the top level. This approach enables us to have
    // platform-specific exclusions, since the two lists are initially processed
    // separately and then merged together afterward.
    for (const key of ['files', 'extraFiles', 'extraResources'] as const) {
      const section = config[electronPlatform];
      const items = config[key];
      const overrideItems = section?.[key];

      if (!section || !Array.isArray(items) || !Array.isArray(overrideItems)) {
        continue;
      }
      config[key] = items.concat(overrideItems);
      delete section[key];
    }

    _.set(config, 'extraMetadata.version', version);

    if (electronPlatform === 'mac') {
      _.set(config, 'dmg.title', `Install Rancher Desktop ${ version }-${ buildUtils.arch }`);
    }

    await fs.promises.writeFile(configPath, yaml.stringify(config), 'utf-8');

    config.afterPack = this.afterPack.bind(this);

    const options: CliOptions = {
      config,
      publish: 'never',
      arm64:   buildUtils.arch === 'arm64',
      x64:     buildUtils.arch === 'x64',
    };

    if (electronPlatform) {
      if (process.argv.includes('--zip')) {
        options[electronPlatform] = ['zip'];
      } else {
        const rawTarget = config[electronPlatform]?.target ?? [];
        const target = Array.isArray(rawTarget) ? rawTarget : [rawTarget];

        options[electronPlatform] = target.map(t => typeof t === 'string' ? t : t.target);
      }
    }

    await build(options);

    return options;
  }

  async buildInstaller(config: CliOptions) {
    const appDir = path.join(buildUtils.distDir, 'win-unpacked');

    if (config.win && !process.argv.includes('--zip')) {
      // Only build installer if we're not asked not to.
      await buildInstaller(buildUtils.distDir, appDir, buildUtils.distDir);
    }
  }

  protected async createLinuxResources(config: Configuration) {
    const appData = 'packaging/linux/rancher-desktop.appdata.xml';
    const input = await fs.promises.readFile(appData, 'utf-8');
    const doc = new DOMParser().parseFromString(input, 'text/xml');

    for (const release of Array.from(doc.getElementsByTagName('release'))) {
      // 0 is not a valid value for SOURCE_DATE_EPOCH; treat it as unset.
      const epoch = Number(process.env.SOURCE_DATE_EPOCH);
      const timestamp = new Date(epoch ? epoch * 1000 : Date.now());
      release.setAttribute('version', config.extraMetadata.version);
      release.setAttribute('date', timestamp.toISOString());
    }
    for (const name of Array.from(doc.getElementsByTagName('name'))) {
      name.textContent = config.productName ?? name.textContent;
    }

    const output = new XMLSerializer().serializeToString(doc);

    await fs.promises.writeFile(appData.replace('packaging', 'resources'), output, 'utf-8');
  }

  protected async createMacResources(workDir: string) {
    // Update signing configuration
    const { productName } = buildUtils.packageMeta;
    const macSigningConfig = yaml.parse(await fs.promises.readFile('build/signing-config-mac.yaml', 'utf-8'));
    for (const override of macSigningConfig.entitlements.overrides) {
      for (const [i, p] of override.paths.entries() as [number, string][]) {
        const baseName = path.basename(p);

        if (/^Rancher Desktop.*\.app$/.test(baseName)) {
          override.paths[i] = path.join(path.dirname(p), baseName.replace(/^Rancher Desktop/, productName));
        }
      }
    }
    await fs.promises.writeFile(
      path.join(workDir, 'signing-config-mac.yaml'),
      yaml.stringify(macSigningConfig), 'utf-8');
  }

  protected async createWindowsResources(workDir: string) {
    // Create the icon
    const imageFile = path.join(process.cwd(), 'resources', 'icons', 'logo-square-512.png');
    const iconFile = path.join(process.cwd(), 'resources', 'icons', 'icon.ico');
    const iconArgs = ['icon', '--format', 'ico', '--out', workDir, '--input', imageFile];
    const iconResult = await this.executeAppBuilderAsJson(iconArgs);
    const outFile = iconResult.icons[0].file;
    await fs.promises.rename(outFile, iconFile);

    // Create the custom action for the installer
    log.info('building Windows Installer custom action...');
    const customActionFile = await buildCustomAction();

    // Wait for the virus scanner to be done with the new DLL file
    for (let i = 0; i < 30; i++) {
      try {
        await fs.promises.readFile(customActionFile);
        break;
      } catch {
        await buildUtils.sleep(5_000);
      }
    }

    // Ensure that the signing config contains the proper name for the excutable.
    // This is needed because the smoke test script uses that config file to find
    // files to check; if it does not match, the smoke tests will fail.
    const { productName } = buildUtils.packageMeta;
    const windowsSigningConfig: Record<string, string[]> =
      yaml.parse(await fs.promises.readFile('build/signing-config-win.yaml', 'utf-8'));
    if (!windowsSigningConfig['.'].includes(`${ productName }.exe`)) {
      throw new Error(`signing-config-win.yaml does not contain the expected executable name: ${ productName }.exe`);
    }
    await fs.promises.writeFile(
      path.join(workDir, 'signing-config-win.yaml'),
      yaml.stringify(windowsSigningConfig), 'utf-8');
  }

  protected async executeAppBuilderAsJson(...args: Parameters<typeof executeAppBuilder>) {
    const result = JSON.parse(await executeAppBuilder(...args));

    if (result.error) {
      throw new Error(result.error);
    }

    return result;
  }

  async run() {
    const options = await this.package();

    await this.buildInstaller(options);
  }
}

(new Builder()).run().catch((e) => {
  console.error(e);
  process.exit(1);
});
