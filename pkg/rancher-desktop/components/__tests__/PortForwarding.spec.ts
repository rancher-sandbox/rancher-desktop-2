// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { shallowMount } from '@vue/test-utils';

import PortForwarding from '@pkg/components/PortForwarding.vue';
import { switchableT } from '@pkg/utils/testUtils/translations';

describe('PortForwarding', () => {
  it('translates its headers after a locale switch', async() => {
    const { t, switchLocale } = switchableT();
    const wrapper = shallowMount(PortForwarding, {
      global: { mocks: { t } },
      props:  { services: [] },
    });

    await switchLocale();
    expect(wrapper.getComponent({ name: 'SortableTable' }).props('headers'))
      .toContainEqual(expect.objectContaining({ label: t('portForwarding.manage.table.header.listenPort') }));
  });
});
