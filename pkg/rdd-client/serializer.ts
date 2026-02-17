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

import { ObjectSerializer as InternalSerializer, V1ObjectMeta } from './gen/models/ObjectSerializer';

interface KubernetesObjectHeader {
  apiVersion: string;
  kind:       string;
  metadata?:  any;
}

const isKubernetesObject = (data: unknown): data is KubernetesObjectHeader =>
  !!data && typeof data === 'object' && 'apiVersion' in data && 'kind' in data;

interface AttributeType {
  name:     string;
  baseName: string;
  type:     string;
  format:   string;
}

class KubernetesObject {
  /**
     * APIVersion defines the versioned schema of this representation of an object. Servers should convert recognized schemas to the latest internal value, and may reject unrecognized values. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources
     */
  'apiVersion'?: string;
  /**
     * Kind is a string value representing the REST resource this object represents. Servers may infer this from the endpoint which the client submits requests to. Cannot be updated. In CamelCase. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds
     */
  'kind'?:       string;
  'metadata'?:   V1ObjectMeta;

  static attributeTypeMap = [
    {
      name:     'apiVersion',
      baseName: 'apiVersion',
      type:     'string',
      format:   '',
    },
    {
      name:     'kind',
      baseName: 'kind',
      type:     'string',
      format:   '',
    },
    {
      name:     'metadata',
      baseName: 'metadata',
      type:     'V1ObjectMeta',
      format:   '',
    },
  ] as const satisfies AttributeType[];

  public serialize(): any {
    const instance: Record<string, any> = {};
    for (const attributeType of KubernetesObject.attributeTypeMap) {
      const value = this[attributeType.baseName];
      if (value !== undefined) {
        instance[attributeType.name] = InternalSerializer.serialize(
          this[attributeType.baseName],
          attributeType.type,
          attributeType.format,
        );
      }
    }
    // add all unknown properties as is.
    for (const [key, value] of Object.entries(this)) {
      if (KubernetesObject.attributeTypeMap.find((t) => t.name === key)) {
        continue;
      }
      instance[key] = value;
    }
    return instance;
  }

  public static fromUnknown(data: unknown): KubernetesObject {
    if (!isKubernetesObject(data)) {
      throw new Error(`Unable to deserialize non-Kubernetes object ${ data }.`);
    }
    const instance = new KubernetesObject();
    for (const attributeType of KubernetesObject.attributeTypeMap) {
      const value = data[attributeType.baseName];
      if (value !== undefined) {
        instance[attributeType.name] = InternalSerializer.deserialize(
          data[attributeType.baseName],
          attributeType.type,
          attributeType.format,
        );
      }
    }
    // add all unknown properties as is.
    for (const [key, value] of Object.entries(data)) {
      if (KubernetesObject.attributeTypeMap.find((t) => t.name === key)) {
        continue;
      }
      instance[key as keyof KubernetesObject] = value;
    }
    return instance;
  }
}

export interface Serializer {
  serialize(data: any, type: string, format?: string): any;
  deserialize(data: any, type: string, format?: string): any;
}

export interface GroupVersionKind {
  group:   string;
  version: string;
  kind:    string;
}

type ModelRegistry = Record<string, Record<string, Serializer>>;

const gvString = ({ group, version }: GroupVersionKind): string => [group, version].join('/');

const gvkFromObject = (obj: KubernetesObjectHeader): GroupVersionKind => {
  const [g, v] = obj.apiVersion.split('/');
  return {
    kind:    obj.kind,
    group:   v ? g : '',
    version: v || g,
  };
};

/**
 * Default serializer that uses the KubernetesObject to serialize and deserialize
 * any object using only the minimum required attributes.
 */
export const defaultSerializer: Serializer = {
  serialize: (data: any, type: string, format?: string): any => {
    if (data instanceof KubernetesObject) {
      return data.serialize();
    }
    return KubernetesObject.fromUnknown(data).serialize();
  },
  deserialize: (data: any, type: string, format?): any => {
    return KubernetesObject.fromUnknown(data);
  },
};

/**
 * Wraps the ObjectSerializer to support custom resources and generic Kubernetes objects.
 *
 * CustomResources that are unknown to the ObjectSerializer can be registered
 * by using ObjectSerializer.registerModel().
 */
export class ObjectSerializer extends InternalSerializer {
  private static modelRegistry: ModelRegistry = {};

  /**
     * Adds a dedicated serializer for a Kubernetes resource.
     * Every resource is uniquely identified using its group, version and kind.
     * @param gvk
     * @param serializer
     */
  public static registerModel(gvk: GroupVersionKind, serializer: Serializer) {
    const gv = gvString(gvk);
    const kinds = (this.modelRegistry[gv] ??= {});
    if (kinds[gvk.kind]) {
      throw new Error(`Kind ${ gvk.kind } of ${ gv } is already defined`);
    }
    kinds[gvk.kind] = serializer;
  }

  /**
     * Removes all registered models from the registry.
     */
  public static clearModelRegistry(): void {
    this.modelRegistry = {};
  }

  private static getSerializerForObject(obj: unknown): undefined | Serializer {
    if (!isKubernetesObject(obj)) {
      return undefined;
    }
    const gvk = gvkFromObject(obj);
    return ObjectSerializer.modelRegistry[gvString(gvk)]?.[gvk.kind];
  }

  public static serialize(data: any, type: string, format = ''): any {
    const serializer = ObjectSerializer.getSerializerForObject(data);
    if (serializer) {
      return serializer.serialize(data, type, format);
    }
    if (data instanceof KubernetesObject) {
      return data.serialize();
    }

    const obj = InternalSerializer.serialize(data, type, format);
    if (obj !== data) {
      return obj;
    }

    if (!isKubernetesObject(data)) {
      return obj;
    }

    const instance: Record<string, any> = {};
    for (const attributeType of KubernetesObject.attributeTypeMap) {
      const value = data[attributeType.baseName];
      if (value !== undefined) {
        instance[attributeType.name] = InternalSerializer.serialize(
          data[attributeType.baseName],
          attributeType.type,
          attributeType.format,
        );
      }
    }
    // add all unknown properties as is.
    for (const [key, value] of Object.entries(data)) {
      if (KubernetesObject.attributeTypeMap.find((t) => t.name === key)) {
        continue;
      }
      instance[key] = value;
    }
    return instance;
  }

  public static deserialize(data: any, type: string, format = ''): any {
    const serializer = ObjectSerializer.getSerializerForObject(data);
    if (serializer) {
      return serializer.deserialize(data, type, format);
    }
    const obj = InternalSerializer.deserialize(data, type, format);
    if (obj !== data) {
      // the serializer knows the type and already deserialized it.
      return obj;
    }

    if (!isKubernetesObject(data)) {
      return obj;
    }

    return KubernetesObject.fromUnknown(data);
  }
}
