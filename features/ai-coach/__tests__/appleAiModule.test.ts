let mockNative: Record<string, unknown> | null;
jest.mock('expo', () => ({ requireOptionalNativeModule: () => mockNative }));

function loadModule() {
  let module!: typeof import('../../../modules/apple-on-device-ai').appleOnDeviceAi;
  jest.isolateModules(() => { module = require('../../../modules/apple-on-device-ai').appleOnDeviceAi; });
  return module;
}

describe('Apple AI context protocol compatibility', () => {
  it.each([undefined, 1, 2])('requires a rebuild for a binary without context prompt version 3 (%s)', async promptVersion => {
    mockNative = { promptVersion, prepareFrames: jest.fn(), getAvailability: jest.fn(async () => 'available') };
    expect(await loadModule().getAvailability('ko')).toBe('module_missing');
    expect(mockNative.getAvailability).not.toHaveBeenCalled();
  });

  it('uses the compatible native module without changing its methods', () => {
    mockNative = { promptVersion: 3, prepareFrames: jest.fn() };
    expect(loadModule()).toBe(mockNative);
  });

  it('keeps unsupported platforms optional', async () => {
    mockNative = null;
    expect(await loadModule().getAvailability('ko')).toBe('module_missing');
  });
});
