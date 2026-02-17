import { defineResource, listNamespacedResource, resourceMutations, resourceState, resourceWatchActions } from '@pkg/store/rddConnection';
import { ActionTree, MutationsType } from '@pkg/store/ts-helpers';
import * as RDDClient from '@rdd-client';

type RDDState = ReturnType<typeof state>;

const resources = [
  defineResource({
    name:       'namespaces',
    path:       '/api/v1/namespaces',
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
    list:       client => client.listNamespace(),
  }),
  defineResource({
    name:       'configMaps',
    path:       '/api/v1/configmaps',
    makeClient: config => config.makeApiClient(RDDClient.CoreV1Api),
    list:       listNamespacedResource('ConfigMap'),
  }),
  defineResource({
    name:       'demos',
    path:       '/apis/app.rancherdesktop.io/v1alpha1/demos',
    makeClient: config => config.makeApiClient(RDDClient.AppRancherdesktopIoV1alpha1Api),
    list:       client => client.listDemo(),
  }),
  defineResource({
    name:       'configMapReplicaSets',
    path:       '/apis/rdd.rancherdesktop.io/v1alpha1/configmapreplicasets',
    makeClient: config => config.makeApiClient(RDDClient.RddRancherdesktopIoV1alpha1Api),
    list:       listNamespacedResource('ConfigMapReplicaSet'),
  }),
  defineResource({
    name:       'notaries',
    type:       'notary',
    path:       '/apis/rdd.rancherdesktop.io/v1alpha1/notaries',
    makeClient: config => config.makeApiClient(RDDClient.RddRancherdesktopIoV1alpha1Api),
    list:       listNamespacedResource('Notary'),
  }),
] as const;

export const state = () => ({
  ...resourceState(resources),
});

export const mutations = {
  ...resourceMutations(resources),
} satisfies MutationsType<RDDState>;

export const actions = {
  ...resourceWatchActions(resources),
} satisfies ActionTree<RDDState, /* root */ any, typeof mutations>;
