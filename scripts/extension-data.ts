/**
 * This file generates pkg/rancher-desktop/assets/extension-data.yaml
 *
 * Usage: `yarn generate:extension-data`
 */

import { generateExtensionMarketplaceData } from './lib/extension-data';

await generateExtensionMarketplaceData();
