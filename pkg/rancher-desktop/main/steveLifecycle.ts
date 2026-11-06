import { Steve } from '@pkg/backend/steve';
import Logging from '@pkg/utils/logging';
import { closeDashboard } from '@pkg/window/dashboard';

const console = Logging.steve;

type StatusConditions = Record<string, readonly [boolean | undefined, ...unknown[]]>;

/**
 * Run Steve while Kubernetes is ready, and close the dashboard when it stops.
 */
export function followAppStatus(statusConditions: StatusConditions) {
  const condition = statusConditions.KubernetesReady;

  // A window reports no conditions until it has read the App.
  if (!condition) {
    return;
  }

  if (condition[0]) {
    Steve.getInstance().start().catch((err) => {
      console.error('Failed to start Steve:', err);
    });
  } else {
    Steve.getInstance().stop();
    // The next Steve listens on a new port, which this window cannot reach.
    closeDashboard();
  }
}
