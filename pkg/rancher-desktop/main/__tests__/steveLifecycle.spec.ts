import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

const steve = { start: jest.fn(() => Promise.resolve(1234)), stop: jest.fn() };
const closeDashboard = jest.fn();

mockModules({
  '@pkg/backend/steve':    { Steve: { getInstance: () => steve } },
  '@pkg/utils/logging':    undefined,
  '@pkg/window/dashboard': { closeDashboard },
});

const { followAppStatus } = await import('@pkg/main/steveLifecycle');

describe('followAppStatus', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('leaves Steve alone for a window that has not read the App yet', () => {
    followAppStatus({});

    expect(steve.start).not.toHaveBeenCalled();
    expect(steve.stop).not.toHaveBeenCalled();
    expect(closeDashboard).not.toHaveBeenCalled();
  });

  it('starts Steve when Kubernetes is ready', () => {
    followAppStatus({ KubernetesReady: [true, 'Ready'] });

    expect(steve.start).toHaveBeenCalled();
    expect(steve.stop).not.toHaveBeenCalled();
    expect(closeDashboard).not.toHaveBeenCalled();
  });

  it.each([false, undefined])('stops Steve and closes the dashboard when KubernetesReady is %s', (status) => {
    followAppStatus({ KubernetesReady: [status, 'NotReady'] });

    expect(steve.stop).toHaveBeenCalled();
    expect(closeDashboard).toHaveBeenCalled();
    expect(steve.start).not.toHaveBeenCalled();
  });
});
