import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

jest.mock('@pkg/window');

const modules = mockModules({
  '@pkg/window': { windowMapping: { } as Record<string, number> },
  electron:      undefined,
});

describe('verifyCertificate', () => {
  function mockGetSystemCertificates(...certs: string[]): () => AsyncIterable<string> {
    return async function * () {
      await Promise.resolve();
      for (const cert of certs) {
        yield cert;
      }
    };
  }

  const returnCodes: Record<string, number> = {
    RESULT_OK:                     0,
    RESULT_USE_CHROMIUM_RESULT:    -3,
  };

  test.concurrent.each`
    verificationResult                     | expected
    ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_OK' }
    ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    `('uses kube certificate for $verificationResult returning $expected',
    async({ verificationResult, expected }) => {
      const callback = jest.fn();
      const { verifyCertificate } = await import('../verify-certificates');
      const kubeCerts = ['test cert'];
      const request = {
        hostname:           '127.0.0.1:8888',
        certificate:        { data: 'test cert', subjectName: 'CN=127.0.0.1', fingerprint: 'abc123' },
        verificationResult,
      } as Partial<Electron.Request> as unknown as Electron.Request;

      await verifyCertificate(kubeCerts, mockGetSystemCertificates(), request, callback);
      expect(callback).toHaveBeenCalledWith(returnCodes[expected]);
    });

  test.concurrent.each`
    verificationResult                     | expected
    ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_OK' }
    ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_OK' }
    ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    `('uses system certificate for $verificationResult returning $expected',
    async({ verificationResult, expected }) => {
      const callback = jest.fn();
      const { verifyCertificate } = await import('../verify-certificates');
      const kubeCerts: string[] = [];
      const request = {
        hostname:           'example.test',
        certificate:        { data: 'system cert', subjectName: 'CN=example.test', fingerprint: 'abc123' },
        verificationResult,
      } as Partial<Electron.Request> as unknown as Electron.Request;

      await verifyCertificate(kubeCerts, mockGetSystemCertificates('system cert'), request, callback);
      expect(callback).toHaveBeenCalledWith(returnCodes[expected]);
    });

  test.concurrent.each`
    verificationResult                     | expected
    ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    `('falls back to default handling for $verificationResult returning $expected',
    async({ verificationResult, expected }) => {
      const callback = jest.fn();
      const { verifyCertificate } = await import('../verify-certificates');
      const kubeCerts: string[] = [];
      const request = {
        hostname:           'example.test',
        certificate:        { data: 'unknown cert', subjectName: 'CN=example.test', fingerprint: 'abc123' },
        verificationResult,
      } as Partial<Electron.Request> as unknown as Electron.Request;

      await verifyCertificate(kubeCerts, mockGetSystemCertificates('system cert'), request, callback);
      expect(callback).toHaveBeenCalledWith(returnCodes[expected]);
    });
});

describe('handleCertificateError', () => {
  describe('plugins dev', () => {
    let originalEnv: NodeJS.ProcessEnv;
    beforeAll(() => {
      originalEnv = { ...process.env };
      process.env.NODE_ENV = 'development';
      process.env.RD_ENV_PLUGINS_DEV = '1';
    });

    afterAll(() => {
      process.env = originalEnv;
    });
    test.each`
      protocol     | host                  | env                | plugins    | expected
      ${ 'https' } | ${ 'localhost:8888' } | ${ 'development' } | ${ true }  | ${ true }
      ${ 'https' } | ${ 'localhost:8888' } | ${ 'development' } | ${ false } | ${ false }
      ${ 'https' } | ${ 'localhost:8888' } | ${ 'production' }  | ${ true }  | ${ false }
      ${ 'https' } | ${ 'localhost:8888' } | ${ 'production' }  | ${ false } | ${ false }
      ${ 'https' } | ${ 'localhost:9443' } | ${ 'development' } | ${ true }  | ${ false }
      ${ 'https' } | ${ 'localhost:9443' } | ${ 'development' } | ${ false } | ${ false }
      ${ 'https' } | ${ 'localhost:9443' } | ${ 'production' }  | ${ true }  | ${ false }
      ${ 'https' } | ${ 'localhost:9443' } | ${ 'production' }  | ${ false } | ${ false }
      ${ 'https' } | ${ '127.0.0.1:8888' } | ${ 'development' } | ${ true }  | ${ false }
      ${ 'https' } | ${ '127.0.0.1:8888' } | ${ 'development' } | ${ false } | ${ false }
      ${ 'https' } | ${ '127.0.0.1:8888' } | ${ 'production' }  | ${ true }  | ${ false }
      ${ 'https' } | ${ '127.0.0.1:8888' } | ${ 'production' }  | ${ false } | ${ false }
      ${ 'wss' }   | ${ 'localhost:8888' } | ${ 'development' } | ${ true }  | ${ true }
      ${ 'wss' }   | ${ 'localhost:8888' } | ${ 'development' } | ${ false } | ${ false }
      ${ 'wss' }   | ${ 'localhost:8888' } | ${ 'production' }  | ${ true }  | ${ false }
      ${ 'wss' }   | ${ 'localhost:8888' } | ${ 'production' }  | ${ false } | ${ false }
      ${ 'wss' }   | ${ 'localhost:9443' } | ${ 'development' } | ${ true }  | ${ false }
      ${ 'wss' }   | ${ 'localhost:9443' } | ${ 'development' } | ${ false } | ${ false }
      ${ 'wss' }   | ${ 'localhost:9443' } | ${ 'production' }  | ${ true }  | ${ false }
      ${ 'wss' }   | ${ 'localhost:9443' } | ${ 'production' }  | ${ false } | ${ false }
      ${ 'wss' }   | ${ '127.0.0.1:8888' } | ${ 'development' } | ${ true }  | ${ false }
      ${ 'wss' }   | ${ '127.0.0.1:8888' } | ${ 'development' } | ${ false } | ${ false }
      ${ 'wss' }   | ${ '127.0.0.1:8888' } | ${ 'production' }  | ${ true }  | ${ false }
      ${ 'wss' }   | ${ '127.0.0.1:8888' } | ${ 'production' }  | ${ false } | ${ false }
      `('$env plugins $plugins on $protocol://$host -> $expected',
      async({ protocol, host, env, plugins, expected }) => {
        const callback = jest.fn();
        const event: Electron.Event = {
          preventDefault: jest.fn(),
        } as unknown as Electron.Event;
        const webContents: Electron.WebContents = {} as unknown as Electron.WebContents;
        const error = '(unused error message)';
        const certificate: Electron.Certificate = {} as unknown as Electron.Certificate;
        const { handleCertificateError } = await import('../verify-certificates');

        process.env.NODE_ENV = env;
        if (plugins) {
          process.env.RD_ENV_PLUGINS_DEV = '1';
        } else {
          delete process.env.RD_ENV_PLUGINS_DEV;
        }
        handleCertificateError(event, webContents, `${ protocol }://${ host }/`, error, certificate, callback);
        expect(callback).toHaveBeenCalledWith(expected);
        if (expected) {
          expect(event.preventDefault).toHaveBeenCalled();
        } else {
          expect(event.preventDefault).not.toHaveBeenCalled();
        }
      });
  });

  describe('dashboard', () => {
    test.each`
      protocol     | host                  | state         | expected
      ${ 'https' } | ${ '127.0.0.1:6120' } | ${ 'open' }   | ${ true }
      ${ 'https' } | ${ '127.0.0.1:6120' } | ${ 'closed' } | ${ false }
      ${ 'https' } | ${ '127.0.0.1:9443' } | ${ 'open' }   | ${ true }
      ${ 'https' } | ${ '127.0.0.1:9443' } | ${ 'closed' } | ${ false }
      ${ 'https' } | ${ '127.0.0.1:8888' } | ${ 'open' }   | ${ false }
      ${ 'https' } | ${ '127.0.0.1:8888' } | ${ 'closed' } | ${ false }
      ${ 'wss' }   | ${ '127.0.0.1:6120' } | ${ 'open' }   | ${ true }
      ${ 'wss' }   | ${ '127.0.0.1:6120' } | ${ 'closed' } | ${ false }
      ${ 'wss' }   | ${ '127.0.0.1:9443' } | ${ 'open' }   | ${ true }
      ${ 'wss' }   | ${ '127.0.0.1:9443' } | ${ 'closed' } | ${ false }
      ${ 'wss' }   | ${ '127.0.0.1:8888' } | ${ 'open' }   | ${ false }
      ${ 'wss' }   | ${ '127.0.0.1:8888' } | ${ 'closed' } | ${ false }
      `('dashboard is $state for $protocol://$host',
      async({ protocol, host, state, expected }) => {
        const callback = jest.fn();
        const event: Electron.Event = {
          preventDefault: jest.fn(),
        } as unknown as Electron.Event;
        const webContents: Electron.WebContents = {} as unknown as Electron.WebContents;
        const error = '(unused error message)';
        const certificate: Electron.Certificate = {} as unknown as Electron.Certificate;
        const { handleCertificateError } = await import('../verify-certificates');

        if (state === 'open') {
          modules['@pkg/window'].windowMapping['dashboard'] = 1;
        } else {
          delete modules['@pkg/window'].windowMapping['dashboard'];
        }
        handleCertificateError(event, webContents, `${ protocol }://${ host }/`, error, certificate, callback);
        expect(callback).toHaveBeenCalledWith(expected);
        if (expected) {
          expect(event.preventDefault).toHaveBeenCalled();
        } else {
          expect(event.preventDefault).not.toHaveBeenCalled();
        }
      });
  });
});
