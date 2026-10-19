import { beforeEach, describe, expect, it, vi } from "vitest";
import { GET, HEAD } from "../../app/api/media/videos/[id]/content/route";
import { DELETE, POST } from "../../app/api/media/videos/session/route";

const videoId = "9f0fb54b-1709-4a0b-8bf1-a6312d4e8d90";
const membershipId = "feee8d6e-03a2-44d0-b248-ad3c78a7d534";
const context = { params: Promise.resolve({ id: videoId }) };
const accessCookie = `inspection_video_session=${encodeURIComponent(JSON.stringify({ accessToken: "private-access-token", membershipId }))}`;

describe("video playback BFF", () => {
  beforeEach(() => {
    vi.stubEnv("NEXT_PUBLIC_INSPECTION_API_URL", "https://api.example.test/graphql");
    vi.stubGlobal("fetch", vi.fn());
  });

  it("IT-553 forwards the authenticated scope through same-origin BFF without a query token", async () => {
    const fetchMock = vi.mocked(fetch).mockResolvedValue(new Response("video-chunk", {
      status: 206,
      headers: {
        "Accept-Ranges": "bytes",
        "Content-Range": "bytes 10-20/100",
        "Content-Length": "11",
        "Content-Type": "video/mp4",
      },
    }));
    const request = new Request(`https://dashboard.example.test/api/media/videos/${videoId}/content?quality=source`, {
      headers: { Cookie: accessCookie, Range: "bytes=10-20" },
    });
    expect(request.headers.get("range")).toBe("bytes=10-20");

    const response = await GET(request, context);

    expect(response.status).toBe(206);
    expect(response.headers.get("cache-control")).toBe("private, no-store");
    expect(response.headers.get("content-range")).toBe("bytes 10-20/100");
    expect(response.headers.get("accept-ranges")).toBe("bytes");
    expect(response.headers.get("content-length")).toBe("11");
    expect(response.headers.get("content-type")).toBe("video/mp4");
    expect(fetchMock).toHaveBeenCalledOnce();
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe(`https://api.example.test/media/videos/${videoId}/content`);
    expect(String(url)).not.toContain("private-access-token");
    expect(new Headers(init?.headers).get("authorization")).toBe("Bearer private-access-token");
    expect(new Headers(init?.headers).get("x-inspection-membership-id")).toBe(membershipId);
    expect((init?.headers as Record<string, string>).Range).toBe("bytes=10-20");
    expect(init?.cache).toBe("no-store");
    expect(await response.text()).toBe("video-chunk");
  });

  it("passes through API authorization and availability statuses without buffering", async () => {
    const fetchMock = vi.mocked(fetch);
    const statuses = [401, 404, 409, 410, 416, 429, 503];
    for (const status of statuses) {
      fetchMock.mockResolvedValueOnce(new Response(null, { status }));
      const request = new Request(`https://dashboard.example.test/api/media/videos/${videoId}/content`, { headers: { Cookie: accessCookie } });
      const response = await GET(request, context);
      expect(response.status).toBe(status);
      expect(response.headers.get("cache-control")).toBe("private, no-store");
    }
  });

  it("sends HEAD upstream and never returns a body", async () => {
    const fetchMock = vi.mocked(fetch).mockResolvedValue(new Response(null, {
      status: 200,
      headers: { "Content-Length": "100", "Content-Type": "video/mp4", "Accept-Ranges": "bytes" },
    }));
    const response = await HEAD(new Request(`https://dashboard.example.test/api/media/videos/${videoId}/content`, {
      method: "HEAD",
      headers: { Cookie: accessCookie },
    }), context);

    expect(response.status).toBe(200);
    expect(response.body).toBeNull();
    expect(response.headers.get("content-length")).toBe("100");
    expect(fetchMock.mock.calls[0][1]?.method).toBe("HEAD");
  });

  it("calls the API again for every range request and returns a newly revoked authorization result", async () => {
    const fetchMock = vi.mocked(fetch)
      .mockResolvedValueOnce(new Response("first range", { status: 206 }))
      .mockResolvedValueOnce(new Response(null, { status: 410 }));
    const makeRequest = (range: string) => new Request(`https://dashboard.example.test/api/media/videos/${videoId}/content`, {
      headers: { Cookie: accessCookie, Range: range },
    });

    expect((await GET(makeRequest("bytes=0-9"), context)).status).toBe(206);
    expect((await GET(makeRequest("bytes=10-19"), context)).status).toBe(410);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect((fetchMock.mock.calls[1][1]?.headers as Record<string, string>).Range).toBe("bytes=10-19");
  });

  it("sets and clears a short-lived httpOnly playback cookie only for same-origin requests", async () => {
    const origin = "https://dashboard.example.test";
    const response = await POST(new Request(`${origin}/api/media/videos/session`, {
      method: "POST",
      headers: { Origin: origin, Authorization: "Bearer private-access-token", "X-Inspection-Membership-ID": membershipId },
    }));
    const cookies = response.headers.get("set-cookie") ?? "";

    expect(response.status).toBe(204);
    expect(cookies).toContain("inspection_video_session=");
    expect(cookies).toContain(encodeURIComponent(JSON.stringify({ accessToken: "private-access-token", membershipId })));
    expect(cookies).toContain("Path=/api/media/videos/");
    expect(cookies).toContain("Max-Age=600");
    expect(cookies).toContain("HttpOnly");
    expect(cookies).toContain("SameSite=Strict");
    expect(cookies).toContain("Secure");

    const forbidden = await POST(new Request(`${origin}/api/media/videos/session`, {
      method: "POST",
      headers: { Origin: "https://attacker.example.test", Authorization: "Bearer private-access-token" },
    }));
    expect(forbidden.status).toBe(403);
    const cleared = await DELETE(new Request(`${origin}/api/media/videos/session`, {
      method: "DELETE",
      headers: { Origin: origin },
    }));
    expect(cleared.headers.get("set-cookie")).toContain("Max-Age=0");
  });

  it("does not call the API when the cookie is missing or the source ID is invalid", async () => {
    const fetchMock = vi.mocked(fetch);
    const missing = await GET(new Request(`https://dashboard.example.test/api/media/videos/${videoId}/content`), context);
    const invalid = await GET(new Request("https://dashboard.example.test/api/media/videos/not-a-uuid/content", { headers: { Cookie: accessCookie } }), {
      params: Promise.resolve({ id: "not-a-uuid" }),
    });

    expect(missing.status).toBe(401);
    expect(invalid.status).toBe(404);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
