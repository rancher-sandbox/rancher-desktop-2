import { jest } from '@jest/globals';

import mockModules from '@pkg/utils/testUtils/mockModules';

const componentStub = { template: '<div />' };

mockModules({
  '@pkg/components/ImagesOutputWindow.vue': componentStub,
  '@pkg/components/ImagesScanResults.vue':  componentStub,
  '@pkg/components/LoadingIndicator.vue':   componentStub,
  '@pkg/utils/imageOutputCuller':           { default: jest.fn() },
  '@pkg/utils/ipcRenderer':                 {
    ipcRenderer: {
      on:   jest.fn(),
      send: jest.fn(),
    },
  },
  '@rancher/components': { Banner: componentStub },
});

const { default: ImageScanDetails } = await import('@pkg/pages/images/scans/_image-name.vue');

describe('image scan details', () => {
  function vulnerabilities(jsonOutput: string): any[] {
    return (ImageScanDetails as any).computed.vulnerabilities.call({ jsonOutput });
  }

  it('keeps generated vulnerability row IDs unique', () => {
    const rows = vulnerabilities(JSON.stringify({
      Results: [
        {
          Vulnerabilities: [
            {
              PkgName:          'stdlib',
              VulnerabilityID:  'CVE-2025-22871',
              InstalledVersion: 'v1.23.0',
            },
            {
              PkgName:          'stdlib',
              VulnerabilityID:  'CVE-2025-22871',
              InstalledVersion: 'v1.24.0',
            },
          ],
        },
      ],
    }));

    expect(rows.map(row => row.id)).toEqual([
      'stdlib-CVE-2025-22871',
      'stdlib-CVE-2025-22871-1',
    ]);
    expect(new Set(rows.map(row => row.id)).size).toBe(rows.length);
  });
});
