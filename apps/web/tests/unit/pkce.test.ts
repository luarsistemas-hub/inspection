import { beforeEach, describe, expect, it, vi } from "vitest";
import { beginPKCE, takePKCEVerifier } from "@/auth/pkce";

describe("PKCE", () => {
  beforeEach(() => {
    sessionStorage.clear();
    vi.stubGlobal("location", { assign: vi.fn() });
  });

  it("binds the verifier to a one-time state value", async () => {
    await beginPKCE("https://id.example/authorize", "inspection-web", "http://localhost:3000/auth/callback");
    const url = new URL(vi.mocked(location.assign).mock.calls[0][0] as string);
    expect(url.searchParams.get("state")).toBeTruthy();
    expect(takePKCEVerifier("wrong-state")).toBeUndefined();
    expect(sessionStorage.getItem("inspection.pkce.verifier")).toBeNull();
  });

  it("returns the verifier once for its matching callback", async () => {
    await beginPKCE("https://id.example/authorize", "inspection-web", "http://localhost:3000/auth/callback");
    const url = new URL(vi.mocked(location.assign).mock.calls[0][0] as string);
    expect(takePKCEVerifier(url.searchParams.get("state"))).toBeTruthy();
    expect(takePKCEVerifier(url.searchParams.get("state"))).toBeUndefined();
  });
});
