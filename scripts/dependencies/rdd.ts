import path from 'node:path';

import { Dependency, DownloadContext } from '@/scripts/lib/dependencies';
import { simpleSpawn } from '@/scripts/simple_process';

export class RDD implements Dependency {
  readonly name = 'rdd';
  async download(context: DownloadContext): Promise<void> {
    const importDir = import.meta.dirname;
    const rddDir = path.join(importDir, '..', '..', 'rdd');

    await simpleSpawn('make', ['build-rdd'], {
      cwd: rddDir,
      env: {
        ...process.env,
        GOOS:   context.goPlatform,
        GOARCH: context.goArch,
      },
    });
  }
}
