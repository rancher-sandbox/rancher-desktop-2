import type { Locator } from '@playwright/test';

/**
 * RDCheckbox returns a locator suitable for a <rd-checkbox>.
 */
export default function RDCheckbox(locator: Locator): Locator {
  const l = locator.locator('input[type="checkbox"]');
  return Object.create(l, {
    // Override `check` to always use force, as Playwright detects that the
    // clicks are handled by a different element.
    check: {
      value: function(options?: Parameters<Locator['check']>[0]) {
        return l.check({ ...options, force: true });
      },
    },
    uncheck: {
      value: function(options?: Parameters<Locator['uncheck']>[0]) {
        return l.uncheck({ ...options, force: true });
      },
    },
  });
}
