<script lang="ts">
import { defineComponent } from 'vue';
export default defineComponent({
  name:  'preferences-nav-item',
  props: {
    /**
     * Indicates if the nav item is active
     */
    active: {
      type:    Boolean,
      default: false,
    },
    /**
     * The Nav Item name
     */
    name: {
      type:     String,
      required: true,
    },
  },
  emits:   ['click'],
  methods: {
    navClicked() {
      this.$emit('click', this.name);
    },
  },
});
</script>

<template>
  <div
    class="preferences-nav-item"
    :class="{ active }"
    @click="navClicked"
  >
    <slot>Menu Item</slot>
  </div>
</template>

<style lang="scss" scoped>
  .preferences-nav-item {
    font-size: 1.125rem;
    line-height: 1.75rem;
    padding: 0.5rem 0.75rem;
    cursor: pointer;
    user-select: none;
  }

  // Solid highlight by default; pre-release builds override these vars with the
  // striped app-icon look (see BODY.prerelease in _light.scss).
  .active {
    background: var(--nav-active-bg, var(--nav-active));
    color: var(--nav-active-fg, inherit);
  }
</style>
