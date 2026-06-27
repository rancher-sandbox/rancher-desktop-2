/**
 * This is the main entry point for Vue.
 */

import Cookies from 'cookie-universal';
import { createApp } from 'vue';

import usePlugins from './plugins';
import router from './router';
import store from './store';

import { isReleaseVersion } from '@pkg/utils/version';

// This does just the Vuex part of cookie-universal-nuxt, which is all we need.
(store as any).$cookies = Cookies();

// Emulate Nuxt layouts by poking making the router match the main component we
// will load, and then inspect it for the layout we set.
// Because we're always using the hash mode for the router, get the correct
// route based on the hash.
const matched = router.resolve(location.hash.substring(1)).matched.find(r => r);
const component = matched?.components?.default;
const layoutName: string = (component as any)?.layout ?? 'default';
const { default: layout } = await import(`../layouts/${ layoutName }.vue`);

// Pre-release builds (development, alpha, beta) carry the striped app-icon look
// into the app windows; Nav.vue and ModalNavItem.vue key off this class.
if (!isReleaseVersion(process.env.RD_VERSION ?? '')) {
  document.body.classList.add('prerelease');
}

const app = createApp(layout);

app.use(store);
app.use(router);
await usePlugins(app, store);

app.mount('#app');
