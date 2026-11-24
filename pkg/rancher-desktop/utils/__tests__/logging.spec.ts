import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import mockModules from '@pkg/utils/testUtils/mockModules';

const logDir = fs.mkdtempSync(path.join(os.tmpdir(), 'rd-logging-'));

mockModules({
  '@pkg/utils/paths': {
    log_dir:    logDir,
    stdout_log: path.join(logDir, 'rdd.stdout.log'),
    stderr_log: path.join(logDir, 'rdd.stderr.log'),
  },
});

const { default: paths } = await import('@pkg/utils/paths');
const { clearLoggingDirectory, Log } = await import('../logging');

afterAll(() => {
  fs.rmSync(logDir, { recursive: true, force: true });
});

describe('clearLoggingDirectory', () => {
  beforeAll(() => {
    Object.defineProperty(process, 'type', { value: 'browser', configurable: true });
  });
  afterAll(() => {
    delete (process as any).type;
  });

  it('keeps the rdd logs and removes stale ones', () => {
    // Without the paths mock, clearLoggingDirectory would delete real log files.
    expect(paths.log_dir).toBe(logDir);
    for (const name of ['rdd.stdout.log', 'rdd.stderr.log', 'stale.log']) {
      fs.writeFileSync(path.join(logDir, name), '');
    }
    clearLoggingDirectory();
    // Loading logging.ts under Jest creates the jest subdirectory.
    expect(fs.readdirSync(logDir).sort()).toEqual(['jest', 'rdd.stderr.log', 'rdd.stdout.log']);
  });
});

describe('Log', () => {
  it('does not truncate a log file in paths.log_dir', async() => {
    const logFile = path.join(logDir, 'unit-test.log');

    fs.writeFileSync(logFile, 'kept');
    const log = new Log('unit-test');

    await log.fdStream;
    expect(log.path).toBe(path.join(logDir, 'jest', 'unit-test.log'));
    expect(fs.readFileSync(logFile, 'utf8')).toBe('kept');
  });
});
