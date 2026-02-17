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

import { ListWatch, type ObjectCache } from './cache';
import { KubeConfig } from './config';
import { KubernetesListObject, KubernetesObject } from './types';
import { Watch } from './watch';

export type ObjectCallback<T extends KubernetesObject> = (obj: T) => void;
export type ErrorCallback = (err?: any) => void;
export type ListCallback<T extends KubernetesObject> = (list: T[], ResourceVersion: string) => void;
export type ListPromise<T extends KubernetesObject> = () => Promise<KubernetesListObject<T>>;

// These are issued per object
export const ADD = 'add';
export type ADD = typeof ADD;
export const UPDATE = 'update';
export type UPDATE = typeof UPDATE;
export const CHANGE = 'change';
export type CHANGE = typeof CHANGE;
export const DELETE = 'delete';
export type DELETE = typeof DELETE;

// This is issued when a watch connects or reconnects
export const CONNECT = 'connect';
export type CONNECT = typeof CONNECT;
// This is issued when there is an error
export const ERROR = 'error';
export type ERROR = typeof ERROR;

export interface Informer<T extends KubernetesObject> {
  on(verb: ADD | UPDATE | DELETE | CHANGE, cb: ObjectCallback<T>): void;
  on(verb: ERROR | CONNECT, cb: ErrorCallback): void;
  off(verb: ADD | UPDATE | DELETE | CHANGE, cb: ObjectCallback<T>): void;
  off(verb: ERROR | CONNECT, cb: ErrorCallback): void;
  start(): Promise<void>;
  stop(): Promise<void>;
}

export function makeInformer<T extends KubernetesObject>(
  kubeconfig: KubeConfig,
  path: string,
  listPromiseFn: ListPromise<T>,
  labelSelector?: string,
): Informer<T> & ObjectCache<T> {
  const watch = new Watch(kubeconfig);
  return new ListWatch<T>(path, watch, listPromiseFn, false, labelSelector);
}
