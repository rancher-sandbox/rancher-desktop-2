<script lang="ts" setup>
import _ from 'lodash';
import { computed, PropType } from 'vue';
import { useStore } from 'vuex';

import type { V1StatusCause } from '@rdd-client';

// TODO: we currently only display errors from V1Status; we also need to support
// states for the VM needs to restart.

const alertMap = {
  reset:   'preferences.actions.banner.reset',
  restart: 'preferences.actions.banner.restart',
  error:   'preferences.actions.banner.error',
} as const;

defineOptions({ name: 'preferences-alert' });

const { severity } = defineProps({
  severity: {
    type:    String as PropType<'restart' | 'error'>,
    default: 'error',
  },
});

const store = useStore();

const errorStatus = computed(() => store.state.preferences.errorStatus);
const preferences = computed(() => store.getters['preferences/preferences']);
const alert = computed(() => severity ? alertMap[severity] : '');
const alertText = computed(() => {
  if (errorStatus.value) {
    if (errorStatus.value.details?.causes?.length) {
      const causes = errorStatus.value.details.causes;
      const causeMessages = causes.map(cause => errorString(cause)).filter(Boolean);
      if (causeMessages.length > 0) {
        return causeMessages.join(', ');
      }
    }
    if (errorStatus.value.message) {
      return errorStatus.value.message;
    }
  }

  if (alert.value) {
    return store.getters['i18n/t'](alert.value, {}) ?? null;
  }

  return null;
});

function errorString(cause: V1StatusCause) {
  if (!cause.field || cause.field === '<nil>') {
    return undefined;
  }
  const keyParts = cause.field.split('.')
    .filter((value, index) => index > 0 || value !== 'spec');
  const key = ['preferences', 'validation', ...keyParts, cause.reason].join('.');
  const current = _.get(preferences.value, keyParts.join('.'));
  const exists = store.getters['i18n/exists'](key);
  const localized = store.getters['i18n/t'](key, { current, message: cause.message });

  return exists ? localized : `${ cause.field }: ${ cause.message }`;
}
</script>

<template>
  <div class="alert">
    <span
      v-if="errorStatus"
      class="alert-text"
    >
      {{ alertText }}
    </span>
  </div>
</template>

<style lang="scss" scoped>
  .alert {
    .alert-text {
      color: var(--body-text);
    }
  }
</style>
