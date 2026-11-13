// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { mount } from '@vue/test-utils';

import LoadingIndicator from '@pkg/components/LoadingIndicator.vue';
import { t } from '@pkg/utils/testUtils/translations';

describe('LoadingIndicator', () => {
  it('translates the icon title', () => {
    // Wrap the English text so a translated title is told apart from a hardcoded one.
    const wrapper = mount(LoadingIndicator, { global: { mocks: { t: (key: string) => `[${ t(key) }]` } } });

    expect(wrapper.find('svg title').text()).toBe('[Loading]');
  });
});
