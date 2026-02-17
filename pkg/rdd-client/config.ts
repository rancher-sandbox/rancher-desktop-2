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

import { Cluster, Context, newClusters, newContexts, newUsers, User } from '@kubernetes/client-node/dist/config_types';
import { parse } from 'yaml';

import { Configuration, createConfiguration, RequestContext, SecurityAuthentication, ServerConfiguration } from './api';

type ApiType = object;
type ApiConstructor<T extends ApiType> = new (config: Configuration) => T;

/**
 * KubeConfig describes how to access the Rancher Desktop Daemon API.
 * Only the limited subset we require is implemented; for example, we only
 * support token authentication.
 * This is mostly API-compatible with @kubernetes/client-node.
 */
export class KubeConfig implements SecurityAuthentication {
  public clusters: Cluster[] = [];
  public users:    User[] = [];
  public contexts: Context[] = [];
  public currentContext = '';

  public getContexts() {
    return this.contexts;
  }

  public getClusters() {
    return this.clusters;
  }

  public getUsers() {
    return this.users;
  }

  public getCurrentContext() {
    return this.currentContext;
  }

  public getContextObject(name: string) {
    return this.contexts.find(c => c.name === name);
  }

  protected getCurrentContextObject() {
    return this.getContextObject(this.currentContext);
  }

  public getCluster(name: string) {
    return this.clusters.find(c => c.name === name);
  }

  public getCurrentCluster() {
    return this.getCluster(this.getCurrentContextObject()?.cluster ?? '');
  }

  public getUser(name: string) {
    return this.users.find(u => u.name === name);
  }

  public getCurrentUser() {
    return this.getUser(this.getCurrentContextObject()?.user ?? '');
  }

  public loadFromString(config: string) {
    const parsed = parse(config);
    if (typeof parsed !== 'object' || parsed?.apiVersion !== 'v1' || parsed?.kind !== 'Config') {
      throw new Error('Invalid kubeconfig');
    }
    this.clusters = newClusters(parsed.clusters);
    this.users = newUsers(parsed.users);
    this.contexts = newContexts(parsed.contexts);
    this.currentContext = parsed['current-context'] ?? '';
  }

  static fromString(config: string) {
    const kubeConfig = new KubeConfig();
    kubeConfig.loadFromString(config);
    return kubeConfig;
  }

  /** @override */
  getName(): string {
    return 'RDD KubeConfig Authentication';
  }

  /** @override */
  applySecurityAuthentication(context: RequestContext) {
    const user = this.getCurrentUser();

    if (user?.token) {
      context.setHeaderParam('Authorization', `Bearer ${ user.token }`);
    }
  }

  public applyToFetchOptions(options?: RequestInit): RequestInit {
    const user = this.getCurrentUser();

    options ??= {};
    options.headers = new Headers(options.headers);

    if (user?.token) {
      options.headers.set('Authorization', `Bearer ${ user.token }`);
    }

    return options;
  }

  makeApiClient<T extends ApiType>(apiClientType: ApiConstructor<T>): T {
    const cluster = this.getCurrentCluster();
    if (!cluster) {
      throw new Error('No active cluster');
    }

    return new apiClientType(createConfiguration({
      baseServer:  new ServerConfiguration(cluster.server, {}),
      authMethods: { default: this },
    }));
  }
}
