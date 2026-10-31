/*
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

import { getSerializationType } from '../util';

describe('getSerializationType', () => {
  it.each([
    ['containers.rancherdesktop.io/v1alpha1', 'Volume', 'IoRancherdesktopContainersV1alpha1Volume'],
    ['v1', 'Namespace', 'V1Namespace'],
    ['apps/v1', 'Deployment', 'V1Deployment'],
    ['example.com/v1beta1', 'Widget', 'V1beta1Widget'],
  ])('maps %s %s to %s', (apiVersion, kind, expected) => {
    expect(getSerializationType(apiVersion, kind)).toEqual(expected);
  });
});
