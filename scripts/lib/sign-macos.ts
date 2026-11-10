/**
 * Code signing support for macOS.
 */

import { createHash } from 'crypto';
import fs from 'fs';
import path from 'path';

import { notarize } from '@electron/notarize';
import { build, Arch, Configuration, Platform } from 'app-builder-lib';
import { MacPackager } from 'app-builder-lib/out/macPackager';
import { AsyncTaskManager, log } from 'builder-util';
import { Target } from 'electron-builder';
import _ from 'lodash';
import * as plist from 'plist';
import yaml from 'yaml';

import buildUtils from '@/scripts/lib/build-utils';
import { spawnFile, SpawnError } from '@pkg/utils/childProcess';
import { appArtifactName, EXT_MACRO, rddArtifactName } from '@pkg/utils/releaseArtifacts';

interface SigningConfig {
  entitlements: {
    default:   string[];
    overrides: {
      paths:         string[];
      // Entitlements; if missing, use the existing entitlements.
      entitlements?: string[];
    }[];
  }
  constraints: {
    paths:        string[];
    self?:        Record<string, any>;
    parent?:      Record<string, any>;
    responsible?: Record<string, any>;
  }[]
  remove: string[];
}

/** The app's rdd, whose entitlements the standalone rdd gets too. */
const APP_RDD = 'Contents/Resources/darwin/bin/rdd';

export async function sign(workDir: string, rddFiles: string[]): Promise<string[]> {
  const certFingerprint = process.env.CSC_FINGERPRINT ?? '';
  const appleId = process.env.APPLEID;
  const appleIdPassword = process.env.AC_PASSWORD;
  const teamId = process.env.AC_TEAMID;

  if (certFingerprint.length < 1) {
    throw new Error(`CSC_FINGERPRINT environment variable not set; required to pick signing certificate.`);
  }

  const unpackedDir = path.join(workDir, 'unpacked');
  const appDirName = (await fs.promises.readdir(unpackedDir, { withFileTypes: true }))
    .find(d => d.isDirectory() && d.name.endsWith('.app'))?.name;

  if (!appDirName) {
    throw new Error(`Could not find .app bundle in ${ unpackedDir }`);
  }
  const appDir = path.join(unpackedDir, appDirName);
  const configPath = path.join(appDir, 'Contents/electron-builder.yml');
  const configText = await fs.promises.readFile(configPath, 'utf-8');
  const config: Configuration = yaml.parse(configText);

  buildUtils.checkArchiveArch(config.mac?.artifactName, config.extraMetadata.version, 'darwin');
  const { version } = config.extraMetadata;
  const rddSource = buildUtils.findStandaloneRdd(rddFiles, version, 'darwin');

  const signingConfigPath = path.join(appDir, 'Contents/build/signing-config-mac.yaml');
  const signingConfigText = await fs.promises.readFile(signingConfigPath, 'utf-8');
  const signingConfig: SigningConfig = yaml.parse(signingConfigText, { merge: true });
  const plistsDir = path.join(workDir, 'plists');
  const signArgs = ['--sign', certFingerprint, '--force', '--timestamp', '--options', 'runtime'];
  let wroteDefaultEntitlements = false;
  let constraintSkipped = false;

  log.info('Removing excess files...');
  await Promise.all(signingConfig.remove.map(async(relpath) => {
    await fs.promises.rm(path.join(appDir, relpath), { recursive: true });
  }));

  log.info('Signing application...');
  // We're not using @electron/osx-sign because it doesn't allow --launch-constraint-*
  await fs.promises.mkdir(plistsDir, { recursive: true });
  for await (const filePath of findFilesToSign(appDir)) {
    const relPath = path.relative(appDir, filePath);
    const fileHash = createHash('sha256').update(relPath, 'utf-8').digest('base64url');
    const args = [...signArgs];

    // Determine the entitlements
    const entitlementsOverride = await overrideEntitlements(signingConfig, appDir, relPath);
    const entitlementName = entitlementsOverride ? fileHash : 'default';
    const entitlements = entitlementsOverride ?? signingConfig.entitlements.default;
    const entitlementFile = path.join(plistsDir, `${ entitlementName }-entitlement.plist`);

    if (!wroteDefaultEntitlements || entitlementName !== 'default') {
      await fs.promises.writeFile(entitlementFile, entitlementsPlist(entitlements));
      wroteDefaultEntitlements ||= entitlementName === 'default';
    }
    args.push('--entitlements', entitlementFile);

    // Determine the launch constraints
    if (process.argv.includes('--skip-constraints')) {
      if (!constraintSkipped) {
        log.warn('Skipping --launch-constraint-...: --skip-constraints given.');
        constraintSkipped = true;
      }
    } else {
      const launchConstraints = signingConfig.constraints.find(c => c.paths.includes(relPath));
      const constraintTypes = ['self', 'parent', 'responsible'] as const;

      for (const constraintType of constraintTypes) {
        const constraint = launchConstraints?.[constraintType];

        if (constraint) {
          const constraintsFile = path.join(plistsDir, `${ fileHash }-constraint-${ constraintType }.plist`);

          await fs.promises.writeFile(constraintsFile, plist.build(evaluateConstraints(constraint)));
          args.push(`--launch-constraint-${ constraintType }`, constraintsFile);
        }
      }
    }

    await spawnFile('codesign', [...args, filePath], { stdio: 'inherit' });
  }

  log.info('Verifying application signature...');
  await spawnFile('codesign', ['--verify', '--deep', '--strict', '--verbose=2', appDir], { stdio: 'inherit' });
  await spawnFile('codesign', ['--display', '--entitlements', '-', appDir], { stdio: 'inherit' });

  // The standalone rdd does not get launch constraints, because it runs
  // outside the app.
  log.info('Signing standalone rdd...');
  const rddTarget = path.join(process.cwd(), 'dist', rddArtifactName(version, 'darwin', buildUtils.arch));
  const rddEntitlements = await overrideEntitlements(signingConfig, appDir, APP_RDD);
  const rddEntitlementFile = path.join(plistsDir, 'rdd-entitlement.plist');

  if (path.resolve(rddSource) !== rddTarget) {
    await fs.promises.copyFile(rddSource, rddTarget, fs.constants.COPYFILE_FICLONE);
  }
  await fs.promises.writeFile(rddEntitlementFile, entitlementsPlist(rddEntitlements ?? signingConfig.entitlements.default));
  await spawnFile('codesign', [...signArgs, '--entitlements', rddEntitlementFile, rddTarget], { stdio: 'inherit' });
  await spawnFile('codesign', ['--verify', '--strict', '--verbose=2', rddTarget], { stdio: 'inherit' });

  if (process.argv.includes('--skip-notarize')) {
    log.warn('Skipping notarization: --skip-notarize given.');
  } else if (appleId && appleIdPassword && teamId) {
    log.info('Notarizing application...');
    await notarize({
      appPath: appDir,
      appleId,
      appleIdPassword,
      teamId,
    });
    log.info('Notarizing standalone rdd...');
    await notarizeFile(rddTarget, workDir, ['--apple-id', appleId, '--password', appleIdPassword, '--team-id', teamId]);
  } else {
    const message = [
      'APPLEID, AC_PASSWORD, or AC_TEAMID environment variables not given, cannot notarize.',
      'To force skip notarization, please pass --skip-notarize to signing script.',
    ];

    throw new Error(message.join('\n'));
  }

  log.info('Building disk image and update archive...');
  const arch = buildUtils.arch === 'arm64' ? Arch.arm64 : Arch.x64;
  const artifactName = appArtifactName(version, 'darwin', buildUtils.arch, EXT_MACRO);
  const formats = ['dmg', 'zip'];

  // Build the dmg, explicitly _not_ using an identity; we just signed
  // everything as we wanted already.
  const results = await build({
    publish: 'never',
    targets: new Map([[Platform.MAC, new Map([[arch, formats]])]]),
    config:  _.merge<Configuration, Configuration>(config,
      {
        dmg: { writeUpdateInfo: false },
        mac: { artifactName, identity: null },
      }),
    prepackaged:             appDir,
    // Provide a custom packager factory so that we can override the packager
    // to skip generating blockmap files.  Generating the blockmap hangs on CI
    // for some reason.
    platformPackagerFactory: (info) => {
      return new CustomPackager(info);
    },
  });

  const dmgFile = results.find(f => f.endsWith('.dmg'));
  const zipFile = results.find(f => f.endsWith('.zip'));

  if (!dmgFile) {
    throw new Error(`Could not find build disk image`);
  }
  if (!zipFile) {
    throw new Error(`Could not find build zip file`);
  }

  await Promise.all([dmgFile, zipFile].map((f) => {
    return spawnFile('codesign', ['--sign', certFingerprint, '--timestamp', f], { stdio: 'inherit' });
  }));

  return [dmgFile, zipFile, rddTarget];
}

/** Build the plist that grants each of the entitlements. */
function entitlementsPlist(entitlements: string[]): string {
  return plist.build(Object.fromEntries(entitlements.map(k => [k, true])));
}

/**
 * Return the entitlements that signingConfig overrides for relPath in the app,
 * or undefined when relPath gets the default ones. An override without an
 * entitlements list keeps the ones the file already has.
 */
async function overrideEntitlements(signingConfig: SigningConfig, appDir: string, relPath: string): Promise<string[] | undefined> {
  const entitlementsOverride = signingConfig.entitlements.overrides.find(e => e.paths.includes(relPath));

  if (!entitlementsOverride) {
    return undefined;
  }
  if (Array.isArray(entitlementsOverride.entitlements)) {
    return entitlementsOverride.entitlements;
  }
  // Use existing entitlements
  const { stdout } = await spawnFile(
    'codesign', ['--display', '--entitlements', '-', '--xml', path.join(appDir, relPath)], { stdio: ['ignore', 'pipe', 'inherit'] });
  const xml = stdout.split(/\r?\n/).find(line => line.startsWith('<?xml'));
  if (!xml) {
    throw new Error(`Could not read existing entitlements from ${ relPath }`);
  }
  const parsed = plist.parse(xml);
  if (parsed && [null, Object.prototype].includes(Object.getPrototypeOf(parsed))) {
    return Object.keys(parsed);
  }
  throw new Error(`Could not parse existing entitlements from ${ relPath }: got ${ JSON.stringify(parsed) }`);
}

/**
 * Notarize a bare executable. Apple cannot staple a ticket to one, so
 * Gatekeeper has to look the ticket up online. The steps follow
 * @electron/notarize, but its notarize() cannot do this, because it always
 * staples.
 * @param credentials The notarytool arguments that authenticate to Apple.
 */
async function notarizeFile(filePath: string, workDir: string, credentials: string[]): Promise<void> {
  const zipPath = path.join(workDir, `${ path.basename(filePath) }.zip`);

  await spawnFile('ditto', ['-c', '-k', '--sequesterRsrc', '--keepParent', filePath, zipPath], { stdio: 'inherit' });

  // Catch notarytool's SpawnError instead of throwing it, because its
  // `command` property holds the password.
  const notarytool = async(args: string[]) => {
    try {
      const { stdout, stderr } = await spawnFile('xcrun', ['notarytool', ...args, ...credentials], { stdio: ['ignore', 'pipe', 'pipe'] });

      return { code: 0, stdout, stderr };
    } catch (ex) {
      if (!(ex instanceof SpawnError)) {
        throw ex;
      }

      return { code: ex.code ?? -1, stdout: ex.stdout ?? '', stderr: ex.stderr ?? '' };
    }
  };
  const result = await notarytool(['submit', zipPath, '--wait', '--output-format', 'json']);
  let parsed: { id?: unknown, status?: unknown };

  try {
    parsed = JSON.parse(result.stdout.trim());
  } catch {
    throw new Error(`Failed to notarize ${ filePath }: notarytool exited with code ${ result.code }:\n${ result.stdout }${ result.stderr }`);
  }
  if (result.code === 0 && parsed.status === 'Accepted') {
    return;
  }

  const message = [`Failed to notarize ${ filePath }: notarytool exited with code ${ result.code }:\n${ result.stdout }${ result.stderr }`];

  if (typeof parsed.id === 'string') {
    const logResult = await notarytool(['log', parsed.id]);

    message.push(`notarytool log:\n${ logResult.stdout }${ logResult.stderr }`);
  }
  throw new Error(message.join('\n\n'));
}

/**
 * Recursively walk the given directory and locate files to sign.
 */
async function * findFilesToSign(dir: string): AsyncIterable<string> {
  // When doing code signing, the children must be signed before their parents
  // (so that their signatures can be incorporated into the parent signature,
  // Merkle tree style).
  // Also, for "Foo.app", we can skip "Foo.app/Contents/MacOS/Foo" because the
  // act of signing the app bundle will sign the executable.
  for (const file of await fs.promises.readdir(dir, { withFileTypes: true })) {
    const fullPath = path.resolve(dir, file.name);

    if (file.isSymbolicLink()) {
      // Skip all symlinks; we sign the symlink target instead.
      continue;
    }
    if (file.isDirectory()) {
      yield * findFilesToSign(fullPath);
    }
    if (!file.isFile()) {
      continue; // We only sign regular files.
    }

    if (await isBundleExecutable(fullPath)) {
      // For bundles (apps and frameworks), we skip signing the executable
      // itself as it will be signed when signing the bundle.
      continue;
    }

    // For regular files, call `file` and check if it thinks it's Mach-O.
    // We previously read the file header, but that was unreliable.
    try {
      const { stdout } = await spawnFile('/usr/bin/file', ['--brief', fullPath], { stdio: 'pipe' });

      if (!stdout.startsWith('Mach-O ')) {
        continue;
      }
    } catch {
      log.info({ fullPath }, 'Failed to read file, assuming no need to sign.');
      continue;
    }

    // If the file is already signed, don't sign it again.
    try {
      await spawnFile('codesign', ['--verify', '--strict=all', '--test-requirement=anchor apple', fullPath]);
      log.info({ fullPath }, 'Skipping signing of already-signed directory');
    } catch {
      yield fullPath;
    }
  }

  if (dir.endsWith('.app') || dir.endsWith('.framework')) {
    // We need to sign app bundles, if they haven't been signed yet.
    try {
      await spawnFile('codesign', ['--verify', '--strict=all', '--test-requirement=anchor apple', dir]);
      log.info({ dir }, 'Skipping signing of already-signed directory');
    } catch {
      yield dir;
    }
  }
}

/**
 * Detect if the path of a plain file indicates that it's the bundle executable
 */
async function isBundleExecutable(fullPath: string): Promise<boolean> {
  const parts = fullPath.split(path.sep).reverse();

  if (parts.length >= 4) {
    // Anything.app/Contents/MacOS/executable - the check style here avoids spell checker.
    if (fullPath.endsWith(`.app/Contents/MacOS/${ parts[0] }`)) {
      // Check Anything.app/Contents/Info.plist for CFBundleExecutable
      const infoPlist = path.sep + path.join(...parts.slice(2).reverse(), 'Info.plist');

      try {
        const executableKey = 'CFBundleExecutable';
        const plistContents = await fs.promises.readFile(infoPlist, 'utf-8');
        const value = plist.parse(plistContents);

        if (typeof value !== 'object' || !(executableKey in value)) {
          return false;
        }

        return value[executableKey] === parts[0];
      } catch (ex) {
        log.info({ ex, infoPlist }, 'Failed to read Info.plist, assuming not the bundle executable.');

        return false;
      }
    }
  }

  if (parts.length >= 4) {
    // Foo.framework/Versions/A/Foo
    if (parts[3] === `${ parts[0] }.framework` && parts[2] === 'Versions') {
      return true;
    }
  }

  return false;
}

/**
 * Given a launch constraint, preprocess it to return values from the environment.
 */
function evaluateConstraints(constraint: Record<string, any>): Record<string, any> {
  return _.mapValues(constraint, (value) => {
    switch (typeof value) {
    case 'string':
      break;
    case 'object':
      if (Array.isArray(value)) {
        return value.map(v => evaluateConstraints(v));
      } else {
        return evaluateConstraints(value);
      }
    default:
      return value;
    }
    switch (value) {
    case '${AC_TEAMID}': // eslint-disable-line no-template-curly-in-string
      return process.env.AC_TEAMID || value;
    default:
      return value;
    }
  });
}

/**
 * CustomPackager overrides MacPackager to avoid building blockmap files
 */
class CustomPackager extends MacPackager {
  override pack(outDir: string, arch: Arch, targets: Target[], taskManager: AsyncTaskManager): Promise<any> {
    for (const target of targets) {
      if ('isWriteUpdateInfo' in target) {
        (target as any).isWriteUpdateInfo = false;
      }
    }

    return super.pack.call(this, outDir, arch, targets, taskManager);
  }
}
