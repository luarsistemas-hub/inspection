import { getAccessToken, getMembershipId } from "@/auth/session";

/** Prepares the short-lived, same-origin cookie used by authenticated video playback. */
export async function prepareVideoPlaybackSession(): Promise<void> {
  const token = getAccessToken();
  if (!token) throw new Error("Autenticação necessária");
  const membershipId = getMembershipId();
  const response = await fetch("/api/media/videos/session", {
    method: "POST",
    credentials: "same-origin",
    cache: "no-store",
    headers: {
      Authorization: `Bearer ${token}`,
      ...(membershipId ? { "X-Inspection-Membership-ID": membershipId } : {}),
    },
  });
  if (!response.ok) throw new Error("Não foi possível preparar a reprodução do vídeo");
}

/** Clears the same-origin playback cookie when the Dashboard session ends. */
export function clearVideoPlaybackSession(): void {
  void fetch("/api/media/videos/session", {
    method: "DELETE",
    credentials: "same-origin",
    cache: "no-store",
    keepalive: true,
  }).catch(() => undefined);
}
