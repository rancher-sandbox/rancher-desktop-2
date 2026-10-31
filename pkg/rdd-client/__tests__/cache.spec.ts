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

import { ListWatch } from '../cache';
import { IoRancherdesktopContainersV1alpha1Volume } from '../gen/models/all';
import { ObjectSerializer } from '../serializer';
import { Watch } from '../watch';

type Volume = IoRancherdesktopContainersV1alpha1Volume;

const apiVersion = 'containers.rancherdesktop.io/v1alpha1';

function volume(name: string, createdAt: string) {
  return {
    apiVersion,
    kind:     'Volume',
    metadata: { name, namespace: 'default', resourceVersion: '1' },
    status:   { createdAt, driver: 'local', mountpoint: `/mnt/${ name }`, scope: 'local' },
  };
}

describe('ListWatch', () => {
  it('stores watch events with the same types as list items', async() => {
    let callback: ((phase: string, obj: any) => void) | undefined;
    // Stands in for the HTTP watch: captures the event callback so the test can feed events.
    const watch = {
      watch: (path: string, query: any, cb: (phase: string, obj: any) => void) => {
        callback = cb;
        return Promise.resolve(new AbortController());
      },
    } as unknown as Watch;
    const listFn = () => Promise.resolve({
      metadata: { resourceVersion: '1' },
      items:    [ObjectSerializer.deserialize(volume('listed', '2026-01-01T00:00:00Z'), 'IoRancherdesktopContainersV1alpha1Volume')],
    });
    const listWatch = new ListWatch<Volume>('/apis/containers.rancherdesktop.io/v1alpha1/volumes', watch, listFn, false);

    await listWatch.start();
    callback!('ADDED', volume('watched', '2026-02-01T00:00:00Z'));

    expect(listWatch.get('listed', 'default')?.status?.createdAt).toBeInstanceOf(Date);
    expect(listWatch.get('watched', 'default')?.status?.createdAt).toEqual(new Date('2026-02-01T00:00:00Z'));
  });
});
