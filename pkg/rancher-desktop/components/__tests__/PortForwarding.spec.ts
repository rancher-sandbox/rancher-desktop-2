// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { shallowMount } from '@vue/test-utils';
import { nextTick } from 'vue';

import PortForwarding from '@pkg/components/PortForwarding.vue';
import { switchableT, t } from '@pkg/utils/testUtils/translations';

describe('PortForwarding', () => {
  it('translates its headers after a locale switch', async() => {
    const translator = switchableT();
    const wrapper = shallowMount(PortForwarding, {
      global: { mocks: { t: translator.t } },
      props:  { services: [] },
    });

    translator.switched.value = true;
    await nextTick();
    expect(wrapper.getComponent({ name: 'SortableTable' }).props('headers'))
      .toContainEqual(expect.objectContaining({ label: `[${ t('portForwarding.manage.table.header.listenPort') }]` }));
  });
});
