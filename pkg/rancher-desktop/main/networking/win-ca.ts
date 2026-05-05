/*
Copyright © 2022 SUSE LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

import tls from 'tls';

/**
 * Asynchronously enumerate the certificate authorities that should be used to
 * build the Rancher Desktop trust store, in PEM format in undefined order.
 */
export default async function * getWinCertificates(): AsyncIterable<string> {
  // Windows will dynamically download CA certificates on demand by default;
  // this means that if we just enumerate the Windows certificate store, we will
  // be missing some standard certificates.  To approximate the desired
  // behaviour, we will enumerate both the Windows store as well as the OpenSSL
  // one built into NodeJS.

  // TODO: Implement, if this is still needed.
  await new Promise((resolve) => resolve);
  yield * tls.rootCertificates;
}
