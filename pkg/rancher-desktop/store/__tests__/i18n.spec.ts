import { jest } from '@jest/globals';

import packageJSON from '@/package.json' with { type: 'json' };
import mockModules from '@pkg/utils/testUtils/mockModules';

mockModules({
  '@pkg/utils/ipcRenderer': {
    ipcRenderer: {
      on:   jest.fn(),
      once: jest.fn(),
      send: jest.fn(),
    },
  },
});

const i18n = await import('@pkg/store/i18n');

// Builds a minimal state shape for exercising the getters directly.
function makeState(translations: Record<string, unknown>, selected = 'de') {
  return {
    default:   'en-us',
    selected,
    available: Object.keys(translations),
    translations,
  };
}

/**
 * Resolves all getters against a state, removing one layer of indirection so
 * that getters which return functions (t, exists, withFallback, …) can be
 * called directly, and getters that depend on other getters receive already-
 * resolved peers.
 */
function makeGetters(state: Record<string, unknown>) {
  let resolved: Record<string, unknown> = {};
  resolved = Object.create(null, Object.fromEntries(Object.entries(i18n.getters).map(([key, getter]) => {
    return [key, {
      get: () => (getter as (...args: any[]) => unknown)(state, resolved),
    }];
  })));

  return resolved as {
    [K in keyof typeof i18n.getters]:
      typeof i18n.getters[K] extends (...args: any[]) => infer R
        ? R
        : never;
  };
}

const en = {
  simple:  'Plain text',
  nested:  { child: 'Nested value' },
  greet:   'Hello {name}',
  plural:  '{count, plural, one {# item} other {# items}}',
  invalid: 'Unbalanced {brace',
  special: 'Command & "Args"',
  product: 'Made by {appName}',
};
const de = {
  simple: 'Einfacher Text',
  greet:  'Hallo {name}',
};

beforeEach(() => {
  // Clear the cache so that each test starts with a clean slate.
  for (const key of Object.keys(i18n._intlCache)) {
    delete i18n._intlCache[key as keyof typeof i18n._intlCache];
  }
});

describe('i18n store getters', () => {
  let state: ReturnType<typeof makeState>;
  let getters: ReturnType<typeof makeGetters>;

  beforeEach(() => {
    state = makeState({ 'en-us': en, de });
    getters = makeGetters(state);
  });

  it('returns the selected locale translation', () => {
    expect(getters.t('simple')).toEqual('Einfacher Text');
  });

  it('falls back to en-us per key', () => {
    expect(getters.t('nested.child')).toEqual('Nested value');
  });

  it('returns a visible %key% placeholder for a missing key', () => {
    expect(getters.t('no.such.key')).toEqual('%no.such.key%');
  });

  it('formats ICU plurals', () => {
    expect(getters.t('plural', { count: 1 })).toEqual('1 item');
    expect(getters.t('plural', { count: 3 })).toEqual('3 items');
  });

  it('interpolates arguments', () => {
    expect(getters.t('greet', { name: 'Jan' })).toEqual('Hallo Jan');
  });

  it('injects the product name for {appName}', () => {
    expect(getters.t('product')).toEqual(`Made by ${ packageJSON.productName }`);
  });

  it('degrades to the raw pattern when an argument is missing', () => {
    const spy = jest.spyOn(console, 'error').mockImplementation(() => {});

    expect(getters.t('plural', {})).toEqual(en.plural);
    spy.mockRestore();
  });

  it('degrades to the raw text for malformed ICU patterns', () => {
    const spy = jest.spyOn(console, 'error').mockImplementation(() => {});

    expect(getters.t('invalid')).toEqual('Unbalanced {brace');
    spy.mockRestore();
  });

  it("does not HTML-escape; escaping is the sink's responsibility", () => {
    expect(getters.t('special')).toEqual('Command & "Args"');
  });

  it('reports key existence', () => {
    expect(getters.exists('simple')).toBe(true);
    expect(getters.exists('no.such.key')).toBe(false);
  });
});

describe('withFallback getter', () => {
  let state: ReturnType<typeof makeState>;
  let getters: ReturnType<typeof makeGetters>;

  beforeEach(() => {
    state = makeState({ 'en-us': en, de });
    getters = makeGetters(state);
  });

  it('returns the selected locale translation', () => {
    expect(getters.withFallback('simple', 'fallback')).toEqual('Einfacher Text');
  });

  it('falls back to en-us per key', () => {
    expect(getters.withFallback('nested.child', 'fallback')).toEqual('Nested value');
  });

  it('returns the fallback string for a missing key', () => {
    expect(getters.withFallback('no.such.key', {}, 'Default value')).toEqual('Default value');
  });

  it('returns the fallback translation for a missing key when fallbackIsKey is true', () => {
    expect(getters.withFallback('no.such.key', {}, 'simple', true)).toEqual('Einfacher Text');
  });
});

describe('availableLocales getter', () => {
  // Codes whose alphabetical order differs from their labels', as in the real
  // locale set: by code ja < ko < pt-br, by label Português < 한국어 < 日本語.
  function scriptGetters(selected: string | null) {
    return makeGetters({
      default:      'en-us',
      selected,
      available:    ['ja', 'ko', 'pt-br'],
      translations: {
        'en-us': {
          locale: {
            ja: 'Japanese', ko: 'Korean', 'pt-br': 'Portuguese (Brazilian)',
          },
        },
        ja:      { locale: { ja: '日本語' } },
        ko:      { locale: { ko: '한국어' } },
        'pt-br': { locale: { 'pt-br': 'Português (Brasil)' } },
      },
    });
  }

  // German collates Ä with A, Swedish sorts it after Z.
  function collationGetters(selected: string) {
    return makeGetters({
      default:      'en-us',
      selected,
      available:    ['de', 'sv'],
      translations: {
        'en-us': { locale: { de: 'Zebra', sv: 'Äpfel' } },
        de:      { locale: { de: 'Zebra' } },
        sv:      { locale: { sv: 'Äpfel' } },
      },
    });
  }

  it('orders locales by label rather than by locale code', () => {
    expect(Object.keys(scriptGetters('en-us').availableLocales)).toEqual(['pt-br', 'ko', 'ja']);
  });

  it('collates in the selected locale', () => {
    expect(Object.keys(collationGetters('de').availableLocales)).toEqual(['sv', 'de']);
    expect(Object.keys(collationGetters('sv').availableLocales)).toEqual(['de', 'sv']);
  });

  it('collates with the default locale before a selection is made', () => {
    expect(Object.keys(scriptGetters(null).availableLocales)).toEqual(['pt-br', 'ko', 'ja']);
  });

  it('collates with the default locale when the selection is not a bundled locale', () => {
    expect(Object.keys(scriptGetters('none').availableLocales)).toEqual(['pt-br', 'ko', 'ja']);
  });
});
