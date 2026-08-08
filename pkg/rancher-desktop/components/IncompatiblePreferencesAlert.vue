<script lang="ts">
import { Banner } from '@rancher/components';
import { PropType, defineComponent } from 'vue';

import { mapTypedActions } from '@pkg/entry/store';
import type { preferencesNavItem as NavItem } from '@pkg/window/preferenceConstants';

export type CompatiblePrefs = {
  /** title is the string to display to the user to describe the preference. */
  title:       string,
  /** navItemName is the nav item (top level navigation) to switch to. */
  navItemName: NavItem['name'];
  /** tabName is the tab to switch to, if any */
  // Just use string until all the tabs exist again, for type checking reasons.
  tabName?:    string // Extract<NavItem, { tabs: unknown }>['tabs'][number][0];
}[];

export default defineComponent({
  name:       'incompatible-preferences-alert',
  components: { Banner },
  props:      {
    compatiblePrefs: {
      type:     Array as PropType<CompatiblePrefs>,
      required: true,
    },
    mode: {
      type:    String as PropType<'selected' | 'disabled'>,
      default: 'selected',
    },
  },
  computed: {
    messagePost(): string {
      switch (this.mode) {
      case 'selected':
        return this.t('preferences.incompatibleTypeWarningPostSelected');
      case 'disabled':
        return this.t('preferences.incompatibleTypeWarningPostDisabled');
      }

      return this.t('preferences.incompatibleTypeWarningPostSelected');
    },
  },
  methods: {
    ...mapTypedActions('transient-preferences', ['navigateByClick']),
  },
});
</script>

<template>
  <banner
    v-if="compatiblePrefs.length > 0"
    color="warning"
  >
    <p>{{ t('preferences.incompatibleTypeWarningPre') }}</p>
    <p
      v-for="(pref, index) in compatiblePrefs"
      :key="index"
    >
      <a
        href="#"
        :data-navigate="pref.tabName ? `${pref.navItemName},${pref.tabName}` : pref.navItemName"
        @click.prevent="navigateByClick"
      >
        {{ pref.title }}
      </a>
      <span v-if="compatiblePrefs.length > 2 && index < (compatiblePrefs.length - 2)">
        {{ ',' }}
      </span>
      <span v-else-if="compatiblePrefs.length >= 2 && index === (compatiblePrefs.length - 2)">
        {{ t('preferences.incompatiblePrefWarningOr') }}
      </span>
    </p>
    <p>{{ messagePost }}</p>
  </banner>
</template>

<style scoped lang="scss">
  :deep(.banner__content) {
    flex-wrap: wrap;
  }
</style>
