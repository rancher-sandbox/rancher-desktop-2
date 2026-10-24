export default {
  name: 'directives',
  install(app) {
    app.directive('focus', {
      mounted(el, _binding, vnode) {
        const { components, refs } = vnode.ctx ?? {};

        if (components && 'LabeledTooltip' in components) {
          refs.value?.focus();
        } else {
          el.focus();
        }
      },
    });
  },
};
