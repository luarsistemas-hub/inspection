import type { CaptureGPS } from "./drafts";

export const CAPTURE_LOCATION_TIMEOUT_MS = 15_000;
// Keep this aligned with MaxGPSAccuracyM in the capture service.
export const MAX_GPS_ACCURACY_METERS = 50;

export type CaptureLocationFailure = "insecure" | "unsupported" | "denied" | "unavailable" | "timeout";
export type CaptureLocationResult = { gps: CaptureGPS; failure?: never } | { gps?: never; failure: CaptureLocationFailure };

/** Requests a fresh position while retaining the photo's guided capture window. */
export function getCaptureLocation(windowStartedAt: string): Promise<CaptureLocationResult> {
  if (!window.isSecureContext) return Promise.resolve({ failure: "insecure" });
  if (!navigator.geolocation) return Promise.resolve({ failure: "unsupported" });

  return new Promise((resolve) => {
    try {
      navigator.geolocation.getCurrentPosition(
        (position) => resolve({ gps: {
          latitude: position.coords.latitude,
          longitude: position.coords.longitude,
          accuracyMeters: position.coords.accuracy,
          capturedAt: windowStartedAt,
          windowStartedAt
        } }),
        (error) => resolve({ failure: error.code === 1 ? "denied" : error.code === 3 ? "timeout" : "unavailable" }),
        { enableHighAccuracy: true, maximumAge: 0, timeout: CAPTURE_LOCATION_TIMEOUT_MS }
      );
    } catch {
      resolve({ failure: "unavailable" });
    }
  });
}
