import type { CameraDevice, CameraDeviceFormat } from "react-native-vision-camera";

export type ResolutionOption = "480p" | "720p" | "1080p" | "2160p";

/**
 * Checks whether a camera format meets 4K resolution (3840x2160 or 2880x2160) at 30fps.
 */
export function is4K30FpsFormat(format: CameraDeviceFormat): boolean {
  const is4KResolution =
    (format.videoWidth >= 3840 && format.videoHeight >= 2160) ||
    (format.videoWidth >= 2160 && format.videoHeight >= 3840);
  const supports30Fps = format.minFps <= 30 && format.maxFps >= 30;

  return is4KResolution && supports30Fps;
}

/**
 * Evaluates whether the given camera device supports 4K (2160p) video recording at 30fps.
 * Applies runtime capability detection across available formats.
 */
export function supports4K30Fps(device?: CameraDevice | null): boolean {
  if (!device || !Array.isArray(device.formats)) {
    return false;
  }
  return device.formats.some(is4K30FpsFormat);
}

/**
 * Checks whether the device supports Video HDR in 4K at 30fps.
 */
export function supports4KVideoHdr(device?: CameraDevice | null): boolean {
  if (!device || !Array.isArray(device.formats)) {
    return false;
  }
  return device.formats.some((f) => is4K30FpsFormat(f) && f.supportsVideoHdr);
}

/**
 * Resolves a safe resolution option using graceful degradation.
 * If 2160p is requested on a device that doesn't support 4K 30fps, falls back to 1080p.
 */
export function resolveSafeResolution(
  requested: string | undefined,
  is4KCapable: boolean
): ResolutionOption {
  if (requested === "2160p") {
    return is4KCapable ? "2160p" : "1080p";
  }
  if (requested === "1080p" || requested === "720p" || requested === "480p") {
    return requested;
  }
  return "720p";
}
