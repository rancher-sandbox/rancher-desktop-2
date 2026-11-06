// This script builds the wix installer, assuming the zip file has already been
// built (and dist/win-unpacked or dist/win-arm64-unpacked is populated).
// This is only used during development.

import fs from 'fs';
import path from 'path';

import buildUtils from './lib/build-utils';
import buildInstaller from './lib/installer-win32';

async function run() {
  const distDir = path.join(process.cwd(), 'dist');
  const appDir = path.join(distDir, `win${ buildUtils.archSuffix }-unpacked`);

  try {
    await fs.promises.access(path.join(appDir, 'resources', 'app.asar'), fs.constants.R_OK);
  } catch (ex) {
    if ((ex as NodeJS.ErrnoException).code !== 'ENOENT') {
      throw ex;
    }
    console.error(`Could not find ${ appDir }, please run \`yarn build\` first.`);
    process.exit(1);
  }

  await buildInstaller(distDir, appDir, distDir);
}

run().catch((ex) => {
  console.error(ex);
  process.exit(1);
});
