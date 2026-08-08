import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';
const onIPCMainProxy = jest.fn((event: unknown, cb: (event: unknown, locale: string) => void) => {});

mockModules({
  '@pkg/utils/logging': undefined,
  '@pkg/main/ipcMain':  {
    getIpcMainProxy: jest.fn(() => ({
      on:   onIPCMainProxy,
      once: jest.fn(),
      send: jest.fn(),
    })),
  },
});

const { availableLocales, initMainI18n, onLocaleChange, t } = await import('@pkg/main/i18n');

describe('main-process i18n', () => {
  it('translates from the default locale', () => {
    expect(t('product.version')).toEqual('Version');
  });

  it('returns a visible %key% placeholder for a missing key', () => {
    expect(t('no.such.key')).toEqual('%no.such.key%');
  });

  it('interpolates arguments', () => {
    expect(t('mainMenu.help.about'))
      .toEqual('&About Rancher Desktop 2');
  });

  it('formats ICU plurals', () => {
    expect(t('sortableTable.paging.generic', { pages: 0 })).toEqual('No Items');
  });

  it('renders ICU quoted literals as visible quotes', () => {
    expect(t('dialog.invalidK8sVersion.message', { version: '1.32' }))
      .toEqual("Requested Kubernetes version '1.32' is not a supported version.");
  });

  it('degrades to the raw pattern when an argument is missing', () => {
    const spy = jest.spyOn(console, 'error').mockImplementation(() => {});

    expect(t('sortableTable.paging.generic', {})).toContain('{pages,');
    spy.mockRestore();
  });

  it('lists the bundled locales', () => {
    expect(availableLocales).toEqual(expect.arrayContaining(['en-us', 'de', 'zh-hans']));
  });

  it('switches locale on IPC events and notifies callbacks', async() => {
    initMainI18n();
    expect(onIPCMainProxy).toHaveBeenCalledWith('i18n/locale-change', expect.any(Function));

    const callback = onIPCMainProxy.mock.calls.find((args) => args[0] === 'i18n/locale-change')?.[1];
    if (!callback) {
      // This exists to satisfy TypeScript (because it doesn't know `callback` is defined).
      throw new Error('Assertion passed, should not be nullish');
    }

    const changed = new Promise<void>((resolve) => {
      const off = onLocaleChange(() => {
        off();
        resolve();
      });
    });

    callback(null, 'de');
    await changed;

    expect(t('generic.cancel')).toEqual('Abbrechen');
    const reverted = new Promise<void>((resolve) => {
      const off = onLocaleChange(() => {
        off();
        resolve();
      });
    });

    callback(null, 'en-us');
    await reverted;

    expect(t('generic.cancel')).toEqual('Cancel');
  });
});
