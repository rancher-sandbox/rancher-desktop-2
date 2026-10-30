// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

/**
 * This file contains the injection keys for use with Vue's provide/inject system.
 */

import type { InjectionKey, Ref } from 'vue';

export const binDirInjectionKey = Symbol('binDir') as InjectionKey<Readonly<Ref<string>>>;
