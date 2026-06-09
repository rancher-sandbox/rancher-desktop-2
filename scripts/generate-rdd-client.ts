/*
Copyright © 2026 SUSE LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// This script generates the RDD client for use with the frontend.
// RDD will be automatically started.

import childProcess from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import stream from 'node:stream';
import url from 'node:url';
import util from 'node:util';

import which from 'which';

import { spawnFile } from '@pkg/utils/childProcess';

const KUBERNETES_BRANCH = '1.35.0';
const KUBERNETES_GEN_COMMIT = 'dde176ff81551585a6986a4aa20b347bd374f03f';

async function run() {
  // The path to the current script.
  const execFile = util.promisify(childProcess.execFile);
  const scriptPath = url.fileURLToPath(import.meta.url);
  const srcDir = path.dirname(path.dirname(scriptPath));
  const clientDir = path.join(srcDir, 'pkg', 'rdd-client');
  const outDir = path.join(clientDir, 'gen');
  const templateDir = path.join(clientDir, 'templates');
  const rddPath = await which('rdd');

  // Fetch the OpenAPI definition.  This requires RDD to be started.
  console.log(`Fetching OpenAPI definition from ${ rddPath }...`);
  const port = await new Promise<number>(resolve => {
    const server = net.createServer();
    server.listen(0, '127.0.0.1', undefined, () => {
      const address = server.address();
      const port = typeof address === 'string' ? 0 : address?.port ?? 0;
      server.close();
      resolve(port);
    });
  });

  if (port === 0) {
    throw new Error('Failed to find free port');
  }

  await fs.promises.rm(outDir, { recursive: true, force: true });
  await fs.promises.mkdir(outDir, { recursive: true });
  await execFile(rddPath, ['service', 'start']);

  try {
    const proxy = childProcess.spawn(rddPath, ['ctl', 'proxy', `--port=${ port }`]);

    try {
      // Wait for the proxy to respond on /healthz
      for (let i = 0; i < 30; i++) {
        try {
          if ((await fetch(`http://127.0.0.1:${ port }/healthz`)).ok) {
            break;
          }
        } catch (ex) {
          // Ignore connection errors, as the proxy might not be up yet.
        }
        await util.promisify(setTimeout)(1_000);
      }

      const response = await fetch(`http://127.0.0.1:${ port }/openapi/v2`);

      if (!response.ok) {
        throw new Error(`Failed to fetch OpenAPI definition: ${ response.statusText }`);
      }
      if (!response.body) {
        throw new Error('No response body when fetching OpenAPI definition');
      }
      await stream.promises.pipeline(
        stream.Readable.fromWeb(response.body as any),
        fs.createWriteStream(path.join(outDir, 'swagger.json.unprocessed')));
    } finally {
      proxy.kill();
    }

    await execFile(rddPath, ['set', 'running=true']);

    // Determine the hash of this file, used as the tag for the images.
    const tagHash = crypto.createHash('sha256');

    await stream.promises.pipeline(fs.createReadStream(scriptPath), tagHash);
    const tag = tagHash.digest('hex');
    const pythonImageBase = `python:3-slim`;
    const pythonImageName = `rdd-client-gen-python:${ tag }`;

    // Build the docker image if needed.
    async function hasImage(imageName: string): Promise<boolean> {
      const { stdout } = await execFile('docker', ['images', '--quiet', imageName]);

      return !!stdout.trim();
    }

    if (!await hasImage(pythonImageName)) {
      console.log(`Building python image ${ pythonImageName }...`);
      const genRepo = 'https://raw.githubusercontent.com/kubernetes-client/gen';
      const dockerFile = new stream.Readable();

      for (const line of [
        `FROM ${ pythonImageBase }`,
        `RUN pip3 install urllib3`,
        `ADD ${ genRepo }/${ KUBERNETES_GEN_COMMIT }/openapi/preprocess_spec.py /`,
        `ADD ${ genRepo }/${ KUBERNETES_GEN_COMMIT }/openapi/custom_objects_spec.json /`,
        `RUN chmod a+r /preprocess_spec.py /custom_objects_spec.json`,
        'ENV OPENAPI_SKIP_FETCH_SPEC=true',
        `ENTRYPOINT ["python3", "/preprocess_spec.py", "typescript", "${ KUBERNETES_BRANCH }", "/out/swagger.json", "kubernetes", "kubernetes"]`,
      ]) {
        dockerFile.push(line + '\n');
      }
      dockerFile.push(null);

      await spawnFile('docker', [
        'build',
        '-',
        '-t', pythonImageName,
      ], { stdio: [dockerFile, 'inherit', 'inherit'] });
    }

    // Generate the models
    console.log('Generating models...');
    await spawnFile('docker', [
      'run',
      '--rm',
      `--user=${ process.getuid?.() ?? 0 }`,
      `--volume=${ outDir }:/out:rw`,
      pythonImageName,
    ], { stdio: 'inherit' });
    await spawnFile('docker', [
      'run',
      '--rm',
      `--user=${ process.getuid?.() ?? 0 }`,
      `--volume=${ outDir }:/output_dir`,
      `--volume=${ templateDir }:/templates:ro`,
      'openapitools/openapi-generator-cli:v7.19.0',
      'generate',
      '--input-spec', '/output_dir/swagger.json',
      '--skip-validate-spec',
      '--generator-name', 'typescript',
      '--import-mappings', 'IntOrString=../../types,V1MicroTime=../../types',
      '--output', '/output_dir',
      '--additional-properties', 'framework=fetch-api',
      '--additional-properties', 'npmName=@rancher/rdd-client',
      '--additional-properties', 'packageAsSourceOnlyLibrary=true',
      '--additional-properties', 'platform=browser',
      '--additional-properties', 'sortParamsByRequiredFlag=true',
      '--additional-properties', 'supportsES6=true',
      '--additional-properties', 'useObjectParameters=true',
      '--additional-properties', `importFileExtension=`,
      '--additional-properties', `modelPropertyNaming=original`,
      '--additional-properties', `npmVersion=0.0.1-${ tag }`,
      '--template-dir', '/templates',
      '--type-mappings', 'int-or-string=IntOrString,date-time-micro=V1MicroTime',
    ], { stdio: 'inherit' });

    console.log('Done.');
  } finally {
    await execFile(rddPath, ['service', 'stop']);
  }
}

await run();
