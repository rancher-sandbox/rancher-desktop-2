/**
 * Jest preloads this file into every test file (see jest.config.js,
 * `setupFiles`) to supply browser APIs jsdom does not implement.
 */

// floating-vue's popper constructs a ResizeObserver when it mounts.
globalThis.ResizeObserver ??= class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
};
