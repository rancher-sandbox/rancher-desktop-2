// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

import { shallowMount } from '@vue/test-utils';

import ImagesScanResults from '@pkg/components/ImagesScanResults.vue';
import { switchableT } from '@pkg/utils/testUtils/translations';

describe('ImagesScanResults', () => {
  it('translates its headers after a locale switch', async() => {
    const { t, switchLocale } = switchableT();
    const wrapper = shallowMount(ImagesScanResults, {
      global: { mocks: { t } },
      props:  { image: 'alpine' },
    });

    await switchLocale();
    expect(wrapper.getComponent({ name: 'SortableTable' }).props('headers'))
      .toContainEqual(expect.objectContaining({ label: t('images.scan.results.headers.package') }));
  });
});
