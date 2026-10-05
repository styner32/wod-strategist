import type { CameraDevice, CameraDeviceFormat } from "react-native-vision-camera";
import {
  is4K30FpsFormat,
  resolveSafeResolution,
  supports4K30Fps,
  supports4KVideoHdr,
} from "../cameraCapability";

describe("cameraCapability", () => {
  const createMockFormat = (
    overrides: Partial<CameraDeviceFormat> = {}
  ): CameraDeviceFormat => ({
    photoHeight: 3000,
    photoWidth: 4000,
    videoHeight: 1080,
    videoWidth: 1920,
    maxISO: 1000,
    minISO: 50,
    fieldOfView: 60,
    supportsVideoHdr: false,
    supportsPhotoHdr: false,
    supportsDepthCapture: false,
    minFps: 1,
    maxFps: 30,
    autoFocusSystem: "phase-detection",
    videoStabilizationModes: ["auto", "cinematic"],
    ...overrides,
  });

  const createMockDevice = (formats: CameraDeviceFormat[]): CameraDevice =>
    ({
      id: "mock-back-camera",
      physicalDevices: ["wide-angle-camera"],
      position: "back",
      name: "Back Camera",
      hasFlash: true,
      hasTorch: true,
      minFocusDistance: 10,
      isMultiCam: false,
      formats,
    }) as unknown as CameraDevice;

  describe("is4K30FpsFormat", () => {
    it("returns true for 3840x2160 at 30fps", () => {
      const format = createMockFormat({
        videoWidth: 3840,
        videoHeight: 2160,
        minFps: 1,
        maxFps: 60,
      });
      expect(is4K30FpsFormat(format)).toBe(true);
    });

    it("returns true for portrait 2160x3840 at 30fps", () => {
      const format = createMockFormat({
        videoWidth: 2160,
        videoHeight: 3840,
        minFps: 15,
        maxFps: 30,
      });
      expect(is4K30FpsFormat(format)).toBe(true);
    });

    it("returns false if resolution is 1080p", () => {
      const format = createMockFormat({
        videoWidth: 1920,
        videoHeight: 1080,
        minFps: 1,
        maxFps: 60,
      });
      expect(is4K30FpsFormat(format)).toBe(false);
    });

    it("returns false if 4K but maxFps is lower than 30", () => {
      const format = createMockFormat({
        videoWidth: 3840,
        videoHeight: 2160,
        minFps: 1,
        maxFps: 24,
      });
      expect(is4K30FpsFormat(format)).toBe(false);
    });

    it("returns false if 4K but minFps is higher than 30", () => {
      const format = createMockFormat({
        videoWidth: 3840,
        videoHeight: 2160,
        minFps: 60,
        maxFps: 120,
      });
      expect(is4K30FpsFormat(format)).toBe(false);
    });
  });

  describe("supports4K30Fps", () => {
    it("returns false for undefined or null device", () => {
      expect(supports4K30Fps(undefined)).toBe(false);
      expect(supports4K30Fps(null)).toBe(false);
    });

    it("returns false when device has only 1080p formats", () => {
      const device = createMockDevice([
        createMockFormat({ videoWidth: 1280, videoHeight: 720 }),
        createMockFormat({ videoWidth: 1920, videoHeight: 1080 }),
      ]);
      expect(supports4K30Fps(device)).toBe(false);
    });

    it("returns true when device has at least one 4K 30fps format", () => {
      const device = createMockDevice([
        createMockFormat({ videoWidth: 1920, videoHeight: 1080 }),
        createMockFormat({ videoWidth: 3840, videoHeight: 2160, minFps: 1, maxFps: 60 }),
      ]);
      expect(supports4K30Fps(device)).toBe(true);
    });
  });

  describe("supports4KVideoHdr", () => {
    it("returns true when a 4K 30fps format supports HDR", () => {
      const device = createMockDevice([
        createMockFormat({
          videoWidth: 3840,
          videoHeight: 2160,
          supportsVideoHdr: true,
        }),
      ]);
      expect(supports4KVideoHdr(device)).toBe(true);
    });

    it("returns false when 4K format does not support HDR", () => {
      const device = createMockDevice([
        createMockFormat({
          videoWidth: 3840,
          videoHeight: 2160,
          supportsVideoHdr: false,
        }),
      ]);
      expect(supports4KVideoHdr(device)).toBe(false);
    });
  });

  describe("resolveSafeResolution", () => {
    it("preserves 2160p when 4K is capable", () => {
      expect(resolveSafeResolution("2160p", true)).toBe("2160p");
    });

    it("falls back from 2160p to 1080p when 4K is NOT capable", () => {
      expect(resolveSafeResolution("2160p", false)).toBe("1080p");
    });

    it("preserves 1080p regardless of 4K capability", () => {
      expect(resolveSafeResolution("1080p", false)).toBe("1080p");
      expect(resolveSafeResolution("1080p", true)).toBe("1080p");
    });

    it("preserves 720p", () => {
      expect(resolveSafeResolution("720p", false)).toBe("720p");
    });

    it("preserves 480p", () => {
      expect(resolveSafeResolution("480p", false)).toBe("480p");
    });

    it("defaults invalid values to 720p", () => {
      expect(resolveSafeResolution("invalid", false)).toBe("720p");
      expect(resolveSafeResolution(undefined, false)).toBe("720p");
    });
  });
});
