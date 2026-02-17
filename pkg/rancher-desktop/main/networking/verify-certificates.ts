import Logging from '@pkg/utils/logging';
import { windowMapping } from '@pkg/window';

const console = Logging.networking;

export default async function verifyCertificate(
  kubeCerts: string[],
  getSystemCertificates: () => AsyncIterable<string>,
  request: Electron.Request,
  callback: (result: number) => void,
) {
  const RESULT_OK = 0;
  const RESULT_USE_CHROMIUM_RESULT = -3;
  const requestInfo = `${ request.hostname } (${ request.certificate.subjectName }/${ request.certificate.fingerprint })`;

  const tlsPort = 9443;
  const dashboardHosts = [`127.0.0.1:${ tlsPort }`, '127.0.0.1:6120'];
  const pluginDevHosts = [`localhost:8888`];

  if (
    process.env.NODE_ENV === 'development' &&
    process.env.RD_ENV_PLUGINS_DEV &&
    pluginDevHosts.includes(request.hostname)
  ) {
    console.debug(`${ request.verificationResult }: Accepting plugin dev cert for ${ requestInfo }`);
    return callback(RESULT_OK);
  }

  if (dashboardHosts.includes(request.hostname) && 'dashboard' in windowMapping) {
    console.debug(`${ request.verificationResult }: Accepting dashboard cert for ${ requestInfo }`);
    return callback(RESULT_OK);
  }

  switch (request.verificationResult) {
  case 'net::ERR_CERT_AUTHORITY_INVALID':
    if (kubeCerts.includes(request.certificate.data.replace(/\r/g, ''))) {
      console.debug(`${ request.verificationResult }: Accepting RDD cert for ${ requestInfo }`);
      return callback(RESULT_OK);
    }
    // Fallthrough
  case 'net::ERR_CERT_INVALID':
    // These errors indicate untrusted certs; ask the system store.
    try {
      for await (const cert of getSystemCertificates()) {
        // For now, just check that the PEM data matches exactly; this is
        // probably a little more strict than necessary, but avoids issues like
        // an attacker generating a cert with the same serial.
        if (cert.replace(/\r/g, '') === request.certificate.data.replace(/\r/g, '')) {
          console.debug(`${ request.verificationResult }: Found system certificate for ${ requestInfo }`);
          return callback(RESULT_OK);
        }
      }
    } catch (ex) {
      console.error(`${ request.verificationResult }: Caught error for ${ requestInfo }: ${ ex }`);
    }
    // Fall through to default handling if we didn't find the cert.
  default:
    // If the certificate is okay, or it's an error we don't want to handle,
    // just pass it through to Chromium's default handling.
    console.debug(`${ request.verificationResult }: Using default for ${ requestInfo }`);
    return callback(RESULT_USE_CHROMIUM_RESULT);
  }
}
