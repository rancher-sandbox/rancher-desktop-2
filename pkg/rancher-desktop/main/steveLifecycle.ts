import { Steve } from '@pkg/backend/steve';
import Logging from '@pkg/utils/logging';
import { closeDashboard } from '@pkg/window/dashboard';

const console = Logging.steve;

type StatusConditions = Record<string, readonly [boolean | undefined, ...unknown[]]>;

/**
 * Start Steve when Kubernetes is ready; otherwise stop Steve and close the
 * dashboard window.
 */
export function followAppStatus(statusConditions: StatusConditions) {
  if (statusConditions.KubernetesReady?.[0]) {
    Steve.getInstance().start().catch((err) => {
      console.error('Failed to start Steve:', err);
    });
  } else {
    Steve.getInstance().stop();
    // The dashboard window connects to this Steve's port, but the next Steve
    // listens on a different one, so the open window would stay disconnected.
    closeDashboard();
  }
}
