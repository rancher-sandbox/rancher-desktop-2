/*
Copyright © 2026 The Kubernetes Authors
Copyright © 2026 SUSE LLC

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

export function findSuffix(quantity: string): string {
  let ix = quantity.length - 1;
  while (ix >= 0 && !/[.0-9]/.test(quantity.charAt(ix))) {
    ix--;
  }
  return ix === -1 ? '' : quantity.substring(ix + 1);
}

export function quantityToScalar(quantity: string): number | bigint {
  if (!quantity) {
    return 0;
  }
  const suffix = findSuffix(quantity);
  if (suffix === '') {
    const num = Number(quantity).valueOf();
    if (isNaN(num)) {
      throw new Error('Unknown quantity ' + quantity);
    }
    return num;
  }
  switch (suffix) {
  case 'n':
    return Number(quantity.substring(0, quantity.length - 1)).valueOf() / 1_000_000_000.0;
  case 'u':
    return Number(quantity.substring(0, quantity.length - 1)).valueOf() / 1_000_000.0;
  case 'm':
    return Number(quantity.substring(0, quantity.length - 1)).valueOf() / 1000.0;
  case 'k':
    return BigInt(quantity.substring(0, quantity.length - 1)) * BigInt(1000);
  case 'M':
    return BigInt(quantity.substring(0, quantity.length - 1)) * BigInt(1000 * 1000);
  case 'G':
    return BigInt(quantity.substring(0, quantity.length - 1)) * BigInt(1000 * 1000 * 1000);
  case 'T':
    return (
      BigInt(quantity.substring(0, quantity.length - 1)) * BigInt(1000 * 1000 * 1000) * BigInt(1000)
    );
  case 'P':
    return (
      BigInt(quantity.substring(0, quantity.length - 1)) *
                BigInt(1000 * 1000 * 1000) *
                BigInt(1000 * 1000)
    );
  case 'E':
    return (
      BigInt(quantity.substring(0, quantity.length - 1)) *
                BigInt(1000 * 1000 * 1000) *
                BigInt(1000 * 1000 * 1000)
    );
  case 'Ki':
    return BigInt(quantity.substring(0, quantity.length - 2)) * BigInt(1024);
  case 'Mi':
    return BigInt(quantity.substring(0, quantity.length - 2)) * BigInt(1024 * 1024);
  case 'Gi':
    return BigInt(quantity.substring(0, quantity.length - 2)) * BigInt(1024 * 1024 * 1024);
  case 'Ti':
    return (
      BigInt(quantity.substring(0, quantity.length - 2)) * BigInt(1024 * 1024 * 1024) * BigInt(1024)
    );
  case 'Pi':
    return (
      BigInt(quantity.substring(0, quantity.length - 2)) *
                BigInt(1024 * 1024 * 1024) *
                BigInt(1024 * 1024)
    );
  case 'Ei':
    return (
      BigInt(quantity.substring(0, quantity.length - 2)) *
                BigInt(1024 * 1024 * 1024) *
                BigInt(1024 * 1024 * 1024)
    );
  default:
    throw new Error(`Unknown suffix: ${ suffix }`);
  }
}

export class ResourceStatus {
  public readonly request:      bigint | number;
  public readonly limit:        bigint | number;
  public readonly resourceType: string;

  constructor(request: bigint | number, limit: bigint | number, resourceType: string) {
    this.request = request;
    this.limit = limit;
    this.resourceType = resourceType;
  }
}

export function add(n1: number | bigint, n2: number | bigint): number | bigint {
  if (typeof n1 === 'number' && typeof n2 === 'number') {
    return n1 + n2;
  }
  if (typeof n1 === 'number') {
    return BigInt(Math.round(n1)) + BigInt(n2);
  } else if (typeof n2 === 'number') {
    return BigInt(n1) + BigInt(Math.round(n2));
  }
  return BigInt(n1) + BigInt(n2);
}

// There is a disconnect between the ApiException headers and the response headers from node-fetch
// ApiException expects { [key: string]: string } whereas node-fetch provides: { [key: string]: string[] }
// https://github.com/node-fetch/node-fetch/issues/783
// https://github.com/node-fetch/node-fetch/pull/1757
export function normalizeResponseHeaders(response: Response): Record<string, string> {
  const normalizedHeaders: any = {};

  for (const [key, value] of response.headers.entries()) {
    normalizedHeaders[key] = value;
  }

  return normalizedHeaders;
}

export function getSerializationType(apiVersion?: string, kind?: string): string {
  if (apiVersion === undefined || kind === undefined) {
    return 'KubernetesObject';
  }
  // Types are defined in src/gen/api/models with the format "<Version><Kind>".
  // Version and Kind are in PascalCase.
  const gv = groupVersion(apiVersion);
  const version = gv.version.charAt(0).toUpperCase() + gv.version.slice(1);
  return `${ version }${ kind }`;
}

interface GroupVersion {
  group:   string;
  version: string;
}

function groupVersion(apiVersion: string): GroupVersion {
  const v = apiVersion.split('/');
  return v.length === 1
    ? {
      group:   'core',
      version: apiVersion,
    }
    : {
      group:   v[0],
      version: v[1],
    };
}
