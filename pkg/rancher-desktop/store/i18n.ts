import { IntlMessageFormat } from 'intl-messageformat';

import packageJson from '../../../package.json' with { type: 'json' };

import { getVendor } from '@pkg/config/private-label';
import type { RootState } from '@pkg/entry/store';
import { ipcRenderer } from '@pkg/utils/ipcRenderer';
import { get } from '@pkg/utils/object';
import { availableLocales, loadTranslations, type LocaleString } from '@pkg/utils/translationLoader';

import type { ActionTree, GetterTree, MutationsType } from './ts-helpers';
import type { MutationTree, Plugin } from 'vuex';

type I18nState = ReturnType<typeof state>;

/**
 * A cache of translation keys (as `<locale>/<key>`) to the corresponding
 * message (or formatter) for the current locale.
 * @note This is only exported for testing; do not use this.
 * @note This is cleared on locale change.
 * @note This is not part of the state because formatter instances do not work
 *   well there; also, this is shared between different states (i.e. windows).
 * @internal
 */
export const _intlCache: Record<`${ LocaleString }/${ string }`, IntlMessageFormat | string> = {};

export const state = () => ({
  default:      'en-us' as LocaleString,
  selected:     null as LocaleString | null,
  available:    [...availableLocales],
  translations: { 'en-us': loadTranslations('en-us') } as Partial<Record<LocaleString, Record<string, unknown>>>,
});

export const getters = {
  availableLocales(state, getters) {
    const current: LocaleString = getters.current;
    const labelled = state.available.map((locale) => {
      const nativeName: string = get(state.translations[locale], `locale.${ locale }`);
      const translatedName: string =
        get(state.translations[current], `locale.${ locale }`) ??
        get(state.translations[state.default], `locale.${ locale }`);

      if ( !nativeName || !translatedName || nativeName === translatedName ) {
        return [locale, nativeName ?? translatedName ?? locale] as const;
      }

      return [locale, `${ nativeName } (${ translatedName })`] as const;
    });

    // Sort by the label the user reads, collated in the selected locale.
    // Fall back to the default when the selection is not a bundled locale,
    // because Intl.Collator rejects an unknown tag. The selection is null
    // until init runs, and older settings can carry 'none'.
    const collationLocale = state.available.includes(current) ? current : state.default;
    const collator = new Intl.Collator(collationLocale);

    labelled.sort(([, a], [, b]) => collator.compare(a, b));

    return Object.fromEntries(labelled) as Record<LocaleString, string>;
  },

  t: (state, getters) => (key: string, args?: Record<string, unknown>) => {
    const current: LocaleString = getters.current;
    const cacheKey = `${ current }/${ key }` as const;
    let formatter = _intlCache[cacheKey];

    if ( !formatter ) {
      let msg = get(state.translations[current], key);

      if ( !msg ) {
        msg = get(state.translations[state.default], key);
      }

      if ( !msg ) {
        // Visible placeholder, matching the main process; missing keys
        // must be debuggable, not silently blank.
        return `%${ key }%`;
      }

      if ( typeof msg === 'object' ) {
        console.error('Translation for', cacheKey, 'is an object');

        return `%${ key }%`;
      }

      if ( typeof msg !== 'string' ) {
        console.error('Translation for', cacheKey, 'is not a string:', msg);

        msg = String(msg);
      }

      if ( msg?.includes('{')) {
        try {
          // Uses the selected locale for formatting even when falling back to
          // English text. Acceptable: plural rules rarely diverge for the
          // strings used here.
          formatter = new IntlMessageFormat(msg, current);
        } catch (e) {
          console.error(`Malformed ICU pattern for key "${ key }":`, e);
          formatter = msg;
        }
      } else {
        formatter = msg;
      }

      _intlCache[cacheKey] = formatter;
    }

    if ( typeof formatter === 'string' ) {
      return formatter;
    } else {
      // Inject things like appName so they're always available in any translation
      const moreArgs = {
        vendor:  getVendor(),
        appName: packageJson.productName,
        ...args,
      };

      try {
        return formatter.format(moreArgs);
      } catch (e) {
        // A missing argument must not abort the component render;
        // degrade to the raw pattern like the main-process interpolator.
        console.error(`Cannot format translation for key "${ key }":`, e);

        return get(state.translations[current], key) ?? get(state.translations[state.default], key);
      }
    }
  },

  exists: (state, getters) => (key: string) => {
    const current: LocaleString = getters.current;
    const cacheKey = `${ current }/${ key }` as const;

    if ( _intlCache[cacheKey] ) {
      return true;
    }

    return [current, state.default].some((locale) => {
      return get(state.translations[locale], key) !== undefined;
    });
  },

  current(state) {
    return state.selected ?? state.default;
  },

  default(state) {
    return state.default;
  },

  withFallback(state, getters) {
    function withFallback(key: string, fallback: string): string;
    function withFallback(key: string, args: Record<string, unknown>, fallback: string, fallbackIsKey?: boolean): string;
    function withFallback(key: string, args: Record<string, unknown> | string, fallback?: string, fallbackIsKey = false) {
      // Support withFallback(key,fallback) when no args
      const parsedFallback = typeof args === 'string' ? args : fallback;
      const parsedArgs = typeof args === 'string' ? {} : args;

      if ( getters.exists(key) ) {
        return getters.t(key, parsedArgs);
      } else if ( parsedFallback === undefined ) {
        console.error(`withFallback called for missing key "${ key }" without a fallback`);

        return `%${ key }%`;
      } else if ( fallbackIsKey ) {
        return getters.t(parsedFallback, parsedArgs);
      } else {
        return parsedFallback;
      }
    }
    return withFallback;
  },
} satisfies GetterTree<I18nState>;

export const mutations = {
  loadTranslations(state, { locale, translations }: { locale: LocaleString, translations: Record<string, unknown> }) {
    state.translations[locale] = translations;
  },

  setSelected(state, locale: LocaleString) {
    state.selected = locale;
  },
} satisfies MutationsType<I18nState> & MutationTree<I18nState>;

export const actions = {
  async init({ state, dispatch }) {
    // Load all translation files so availableLocales can show native names.
    // Acceptable overhead with a small number of locales; revisit if locale
    // count grows significantly.
    await Promise.allSettled(
      state.available
        .filter(locale => !state.translations[locale])
        .map(locale => dispatch('load', locale)),
    );

    // Default to the locale stored in local storage; we will pick up the
    // locale from the app once that exists.
    const selected = localStorage.getItem('locale') as LocaleString | null || state.default;

    return dispatch('switchTo', selected);
  },

  load({ commit }, locale: LocaleString) {
    const translations = loadTranslations(locale);

    commit('loadTranslations', { locale, translations });

    return true;
  },

  async switchTo({ state, commit, dispatch }, locale: LocaleString) {
    if ( !locale ) {
      locale = state.default;
    }

    if ( !state.translations[locale] ) {
      try {
        await dispatch('load', locale);
      } catch (e) {
        console.error(`Failed to load translations for locale "${ locale }":`, e);
        if ( locale !== 'en-us' ) {
          // Try to show something...
          locale = 'en-us';
        }
      }
    }

    for (const key of Object.keys(_intlCache)) {
      delete _intlCache[key as keyof typeof _intlCache];
    }

    commit('setSelected', locale);
    localStorage.setItem('locale', locale);
    ipcRenderer.send('i18n/locale-change', locale);
  },

} satisfies ActionTree<I18nState, RootState, typeof mutations, typeof getters>;

export const plugins: Plugin<RootState>[] = [
  // Update the selected locale when it has been set from the preferences.
  function(store) {
    store.watch(
      (state, getters) => getters['preferences/preferences']?.application?.locale,
      (newLocale: LocaleString | undefined) => {
        if (newLocale && newLocale !== store.state.i18n.selected) {
          store.dispatch('i18n/switchTo', newLocale);
        }
      },
    );
  },
  // Update the _displayed_ locale when the local storage value has changed;
  // this happens to the main window when the preferences dialog was changed
  // without saving.
  function(store) {
    addEventListener('storage', (event) => {
      if ( event.key === 'locale' ) {
        const newLocale = event.newValue as LocaleString | null;

        if (newLocale && newLocale !== store.state.i18n.selected) {
          store.dispatch('i18n/switchTo', newLocale);
        }
      }
    });
  },
];
