import Electron from 'electron';

import Logging from '@pkg/utils/logging';

const console = Logging.networking;

/**
 * Clean up PEM-encoded certificate data, normalizing line endings and trimming
 * whitespace.
 */
export function cleanupCert(cert: string): string {
  return cert.replace(/\r\n?/g, '\n').trim();
}

/**
 * Implementation of Electron's `setCertificateVerifyProc` callback for
 * verifying certificates.  This is used to allow RDD's control plane
 * certificates as well as system certificates to be accepted.
 * @param rddCerts PEM-encoded RDD control plane certificates; any certificates
 *  in this list should be trimmed and use LF line endings.
 * @param getSystemCertificates Function returning system certificates as
 *  trimmed PEM-encoded strings with LF line endings.
 * @param request The request for which the certificate is being verified.
 * @param callback The callback to call with the result of the verification.
 */
export async function verifyCertificate(
  rddCerts: string[],
  getSystemCertificates: () => AsyncIterable<string>,
  request: Electron.Request,
  callback: (result: number) => void,
) {
  const RESULT_OK = 0;
  const RESULT_USE_CHROMIUM_RESULT = -3;
  const requestInfo = `${ request.hostname } (${ request.certificate.subjectName }/${ request.certificate.fingerprint })`;
  const requestCert = cleanupCert(request.certificate.data);

  // Because `request.hostname` does not include the port, plugin development is
  // handled in the `certificate-error` event handler.

  switch (request.verificationResult) {
  case 'net::ERR_CERT_AUTHORITY_INVALID':
    if (rddCerts.includes(requestCert)) {
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
        if (cleanupCert(cert) === requestCert) {
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

/**
 * Implementation of Electron's `certificate-error` event handler for handling
 * certificate errors.
 * @param rddCerts PEM-encoded RDD control plane certificates; any certificates
 *  in this list should be trimmed and use LF line endings.
 * @param event The event that triggered the error.
 * @param webContents The web contents that triggered the error.
 * @param url The URL that triggered the error.
 * @param error The error that occurred.
 * @param certificate The certificate that triggered the error.
 * @param callback The callback to call with whether the certificate should be
 *  trusted.
 */
export function handleCertificateError(
  rddCerts: string[],
  event: Electron.Event,
  webContents: Electron.WebContents,
  url: string,
  error: string,
  certificate: Electron.Certificate,
  callback: (isTrusted: boolean) => void,
) {
  const allowedHostPorts: string[] = [];
  // Plugins development URLs
  if (process.env.NODE_ENV === 'development' && process.env.RD_ENV_PLUGINS_DEV) {
    allowedHostPorts.push('localhost:8888');
  }

  for (const hostPort of allowedHostPorts) {
    if ([`https://${ hostPort }/`, `wss://${ hostPort }/`].some(x => url.startsWith(x))) {
      event.preventDefault();
      callback(true);

      return;
    }
  }

  if (error === 'net::ERR_CERT_AUTHORITY_INVALID') {
    if (rddCerts.includes(cleanupCert(certificate.data))) {
      event.preventDefault();
      callback(true);

      return;
    }
  }

  console.log(`Not handling certificate error ${ error } for ${ url }`);

  callback(false);
}
