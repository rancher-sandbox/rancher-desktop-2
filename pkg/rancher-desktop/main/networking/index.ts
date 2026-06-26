import dns from 'dns';
import http from 'http';
import https from 'https';
import os from 'os';
import util from 'util';

import Electron from 'electron';

import getLinuxCertificates from './linux-ca';
import getMacCertificates from './mac-ca';
import ElectronProxyAgent from './proxy';
import { cleanupCert, handleCertificateError, verifyCertificate } from './verify-certificates';
import getWinCertificates from './win-ca';

import mainEvents from '@pkg/main/mainEvents';
import Logging from '@pkg/utils/logging';
import { KubeConfig } from '@rdd-client';

const console = Logging.networking;

export default async function setupNetworking() {
  const agentOptions = { ...https.globalAgent.options };

  if (!Array.isArray(agentOptions.ca)) {
    agentOptions.ca = agentOptions.ca ? [agentOptions.ca] : [];
  }
  try {
    for await (const cert of getSystemCertificates()) {
      agentOptions.ca.push(cert);
    }
  } catch (ex) {
    console.error('Error getting system certificates:', ex);
    throw ex;
  }

  const proxyAgent = new ElectronProxyAgent({
    httpAgent:  new http.Agent(agentOptions),
    httpsAgent: new https.Agent(agentOptions),
  });

  http.globalAgent = proxyAgent;
  https.globalAgent = proxyAgent;

  mainEvents.handle('rdd/kube-config-ready', (kubeConfigStr) => {
    const kubeConfig = new KubeConfig();
    kubeConfig.loadFromString(kubeConfigStr);
    configureRDDAuthentication(kubeConfig);
    const kubeCerts =
      Buffer.from(kubeConfig.getCurrentCluster()?.caData ?? '', 'base64')
        .toString('utf-8')
        .split(/(?=-----BEGIN CERTIFICATE-----)/g)
        .map(cleanupCert)
        .filter(x => x);

    // Set up certificate handling for specific hosts; we ignore the certificate
    // completely on these, but limit them to specific ports.
    Electron.app.on('certificate-error', handleCertificateError.bind(null, kubeCerts));
    // Set up certificate handling for system certificates, as well as handling
    // custom certificate authorities for RDD.
    Electron.session.defaultSession.setCertificateVerifyProc(
      verifyCertificate.bind(null, kubeCerts, getSystemCertificates));

    mainEvents.emit('network-ready');

    return Promise.resolve();
  });
  // Trigger RDD config loading, ignoring the result.
  mainEvents.invoke('rdd/kube-config').catch(console.error);
}

/**
 * Configure the default Electron session's WebRequest to provide authentication
 * for accessing RDD's API server.
 * @param serverURL The URL of the API server.
 */
function configureRDDAuthentication(kubeConfig: KubeConfig) {
  const server = kubeConfig.getCurrentCluster()?.server;

  if (!server) {
    throw new Error('No currently active cluster');
  }

  const origin = (new URL(server)).origin;
  const urls = [`${ server }/*`, `${ server.replace(/^http/, 'ws') }/passthrough/*`];
  const { webRequest } = Electron.session.defaultSession;

  // Attach the authorization headers here because the WebSocket API does not
  // allow setting it.  Might as well do the REST headers here too.
  webRequest.onBeforeSendHeaders(
    { urls },
    (details, callback) => {
      callback({
        requestHeaders: {
          ...details.requestHeaders,
          Authorization: `Bearer ${ kubeConfig.getCurrentUser()?.token ?? '' }`,
          Origin:        origin,
          'User-Agent':  `RancherDesktopFrontend/${ Electron.app.getVersion() } (${ os.platform() })`,
        },
      });
    });
  // Override CORS headers.
  webRequest.onHeadersReceived(
    { urls },
    (details, callback) => {
      const statusLine = details.method === 'OPTIONS' ? 'HTTP/1.1 204' : details.statusLine;
      callback({
        statusLine,
        responseHeaders: {
          ...details.responseHeaders,
          'Access-Control-Allow-Origin':  '*',
          'Access-Control-Allow-Methods': 'GET, POST, PATCH, PUT, DELETE, OPTIONS',
          'Access-Control-Allow-Headers': 'Authorization, Content-Type',
        },
      });
    });
}

/**
 * Get the system certificates in PEM format.
 */
export async function * getSystemCertificates(): AsyncIterable<string> {
  const platform = os.platform();

  if (platform.startsWith('win')) {
    yield * getWinCertificates();
  } else if (platform === 'darwin') {
    yield * getMacCertificates();
  } else if (platform === 'linux') {
    yield * getLinuxCertificates();
  } else {
    throw new Error(`Cannot get system certificates on ${ platform }`);
  }
}

export async function checkConnectivity(target: string): Promise<boolean> {
  try {
    await util.promisify(dns.lookup)(target);

    return true;
  } catch {
    return false;
  }
}
