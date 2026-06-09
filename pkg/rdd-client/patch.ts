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

/**
 * Valid Content-Type header values for patch operations.  See
 * https://kubernetes.io/docs/tasks/run-application/update-api-object-kubectl-patch/
 * for details.
 *
 * Additionally for Server-Side Apply https://kubernetes.io/docs/reference/using-api/server-side-apply/
 * and https://kubernetes.io/docs/reference/using-api/server-side-apply/#api-implementation
 */
export const PatchStrategy = {
  /** Diff-like JSON format. */
  JsonPatch:           'application/json-patch+json',
  /** Simple merge. */
  MergePatch:          'application/merge-patch+json',
  /** Merge with different strategies depending on field metadata. */
  StrategicMergePatch: 'application/strategic-merge-patch+json',
  /** Server-Side Apply */
  ServerSideApply:     'application/apply-patch+yaml',
} as const;

export type PatchStrategy = (typeof PatchStrategy)[keyof typeof PatchStrategy];
