/**
 * Shared translation helper for tests and mocks.
 *
 * Loads the English YAML once and provides a t() function using the same
 * ICU MessageFormat engine as the renderer and the main process.
 */

import { IntlMessageFormat } from 'intl-messageformat';
import { nextTick, ref } from 'vue';

import { availableLocales, loadTranslations } from '../translationLoader';

type TranslationMap = Record<string, unknown>;

const en = loadTranslations('en-us') as TranslationMap;

export { availableLocales };

function getByPath(obj: TranslationMap, keyPath: string): string | undefined {
  let current: unknown = obj;

  for (const segment of keyPath.split('.')) {
    if (current == null || typeof current !== 'object') {
      return undefined;
    }
    current = (current as Record<string, unknown>)[segment];
  }

  return typeof current === 'string' ? current : undefined;
}

export function t(key: string, args?: Record<string, string | number>): string {
  const msg = getByPath(en, key);

  if (msg === undefined) {
    return `%${ key }%`;
  }

  try {
    return new IntlMessageFormat(msg, 'en-us').format(args) as string;
  } catch {
    return msg;
  }
}

/**
 * Return a t() for tests, and a switchLocale() that fakes a locale switch.
 * Before the switch, t() returns the English text.  After it, t() wraps
 * every translation in brackets, which stands in for another locale's
 * text.  Call t() after switchLocale() to get the text that a component
 * should then show.  Each call reads the switch state, so when
 * switchLocale() changes it, Vue recomputes every computed property that
 * translates through this t().  switchLocale() resolves once Vue has
 * re-rendered.  In the app, a computed property that translates through
 * the store's `i18n/t` getter recomputes the same way after a real locale
 * switch.
 */
export function switchableT() {
  const switched = ref(false);

  return {
    t(key: string, args?: Record<string, string | number>): string {
      return switched.value ? `[${ t(key, args) }]` : t(key, args);
    },
    async switchLocale() {
      switched.value = true;
      await nextTick();
    },
  };
}
