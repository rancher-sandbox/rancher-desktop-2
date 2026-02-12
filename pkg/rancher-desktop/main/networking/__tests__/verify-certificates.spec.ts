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
      verificationResult                     | expected
      ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_OK' }
      ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_OK' }
      ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_OK' }
      `('accepts any certificate for $verificationResult',
      async({ verificationResult, expected }) => {
        const callback = jest.fn();
        const { default: verifyCertificate } = await import('../verify-certificates');
        const kubeCerts: string[] = [];
        const request = {
          hostname:           'localhost:8888',
          certificate:        { data: 'dev cert', subjectName: 'CN=localhost', fingerprint: 'abc123' },
          verificationResult,
        } as Partial<Electron.Request> as unknown as Electron.Request;

        process.env.NODE_ENV = 'development';
        process.env.RD_ENV_PLUGINS_DEV = '1';
        await verifyCertificate(kubeCerts, mockGetSystemCertificates(), request, callback);
        expect(callback).toHaveBeenCalledWith(returnCodes[expected]);
      });
  });

  describe('dashboard', () => {
    test.each`
      hostname              | state         | verificationResult                     | expected
      ${ '127.0.0.1:6120' } | ${ 'open' }   | ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_OK' }
      ${ '127.0.0.1:6120' } | ${ 'open' }   | ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_OK' }
      ${ '127.0.0.1:6120' } | ${ 'open' }   | ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_OK' }
      ${ '127.0.0.1:6120' } | ${ 'closed' } | ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_USE_CHROMIUM_RESULT' }
      ${ '127.0.0.1:6120' } | ${ 'closed' } | ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
      ${ '127.0.0.1:6120' } | ${ 'closed' } | ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
      ${ '127.0.0.1:9443' } | ${ 'open' }   | ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_OK' }
      ${ '127.0.0.1:9443' } | ${ 'open' }   | ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_OK' }
      ${ '127.0.0.1:9443' } | ${ 'open' }   | ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_OK' }
      ${ '127.0.0.1:9443' } | ${ 'closed' } | ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_USE_CHROMIUM_RESULT' }
      ${ '127.0.0.1:9443' } | ${ 'closed' } | ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
      ${ '127.0.0.1:9443' } | ${ 'closed' } | ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
      `('dashboard is $state for $verificationResult on $hostname',
      async({ hostname, state, verificationResult, expected }) => {
        const callback = jest.fn();
        const { default: verifyCertificate } = await import('../verify-certificates');
        const kubeCerts: string[] = [];
        const request = {
          hostname,
          certificate: { data: 'dashboard cert', subjectName: 'CN=example.com', fingerprint: 'abc123' },
          verificationResult,
        } as Partial<Electron.Request> as unknown as Electron.Request;

        if (state === 'open') {
          modules['@pkg/window'].windowMapping['dashboard'] = 1;
        } else {
          delete modules['@pkg/window'].windowMapping['dashboard'];
        }
        await verifyCertificate(kubeCerts, mockGetSystemCertificates(), request, callback);
        expect(callback).toHaveBeenCalledWith(returnCodes[expected]);
      });
  });

  test.each`
    verificationResult                     | expected
    ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_OK' }
    ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    `('uses kube certificate for $verificationResult returning $expected',
    async({ verificationResult, expected }) => {
      const callback = jest.fn();
      const { default: verifyCertificate } = await import('../verify-certificates');
      const kubeCerts = ['test cert'];
      const request = {
        hostname:           '127.0.0.1:8888',
        certificate:        { data: 'test cert', subjectName: 'CN=127.0.0.1', fingerprint: 'abc123' },
        verificationResult,
      } as Partial<Electron.Request> as unknown as Electron.Request;

      await verifyCertificate(kubeCerts, mockGetSystemCertificates(), request, callback);
      expect(callback).toHaveBeenCalledWith(returnCodes[expected]);
    });

  test.each`
    verificationResult                     | expected
    ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_OK' }
    ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_OK' }
    ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    `('uses system certificate for $verificationResult returning $expected',
    async({ verificationResult, expected }) => {
      const callback = jest.fn();
      const { default: verifyCertificate } = await import('../verify-certificates');
      const kubeCerts: string[] = [];
      const request = {
        hostname:           'example.test',
        certificate:        { data: 'system cert', subjectName: 'CN=example.test', fingerprint: 'abc123' },
        verificationResult,
      } as Partial<Electron.Request> as unknown as Electron.Request;

      await verifyCertificate(kubeCerts, mockGetSystemCertificates('system cert'), request, callback);
      expect(callback).toHaveBeenCalledWith(returnCodes[expected]);
    });

  test.each`
    verificationResult                     | expected
    ${ 'net::ERR_CERT_AUTHORITY_INVALID' } | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    ${ 'net::ERR_CERT_INVALID' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    ${ 'net::ERR_CERT_REVOKED' }           | ${ 'RESULT_USE_CHROMIUM_RESULT' }
    `('falls back to default handling for $verificationResult returning $expected',
    async({ verificationResult, expected }) => {
      const callback = jest.fn();
      const { default: verifyCertificate } = await import('../verify-certificates');
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
