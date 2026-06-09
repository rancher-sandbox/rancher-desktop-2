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

import { V1ListMeta, V1ObjectMeta } from './api.js';

export interface KubernetesObject {
  apiVersion?: string;
  kind?:       string;
  metadata?:   V1ObjectMeta;
}

export interface KubernetesListObject<T extends KubernetesObject> {
  apiVersion?: string;
  kind?:       string;
  metadata?:   V1ListMeta;
  items:       T[];
}

export type IntOrString = number | string;

export class V1MicroTime extends Date {
  public toISOString(): string {
    return super.toISOString().slice(0, -1) + '000Z';
  }
}
