/**
 * Records named points during application startup and logs them once, as
 * milliseconds since the main process was created, so that the log of a slow
 * launch shows where the time went.
 *
 * Import only types (`import type`) from logging and paths, never values.  The
 * paths module imports this one while it loads, and logging imports paths, so
 * a value import from either would be circular.  TypeScript removes type-only
 * imports from the compiled code, so they don't create that cycle.
 */

import type { Log } from '@pkg/utils/logging';

interface StartupMark {
  name: string;
  time: number;
}

const marks: StartupMark[] = [];
let reported = false;

/**
 * Record the current time under the given name.  Ignored once the report has
 * been written.
 */
export function markStartup(name: string) {
  if (!reported) {
    marks.push({ name, time: Date.now() });
  }
}

/**
 * Write all recorded marks to the given log as one line.  Only the first call
 * writes anything.
 */
export function reportStartup(log: Log) {
  if (reported) {
    return;
  }
  reported = true;

  const creationTime = process.getCreationTime();
  const origin = creationTime ?? marks[0]?.time ?? 0;
  const description = creationTime === null ? 'ms since first mark; process creation time unavailable' : 'ms since process creation';
  const entries = marks.map(({ name, time }) => `${ name }=${ Math.round(time - origin) }`);

  log.log(`Startup timing (${ description }): ${ entries.join(', ') }`);
}
