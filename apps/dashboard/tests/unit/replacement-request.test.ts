import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { graphql } = vi.hoisted(() => ({ graphql: vi.fn() }));
vi.mock("@/graphql/client", () => ({ graphql }));

import { ReplacementRequest, defaultReplacementDeadline, parseReplacementInput } from "@/features/dashboard/replacement-request";

const now = new Date("2026-10-19T12:00:00Z");

describe("parseReplacementInput", () => {
  it("accepts a future deadline within 30 days and a reason", () => {
    expect(parseReplacementInput("2026-10-22T12:00", "Regravar o vídeo", now)).toEqual({ deadlineAt: new Date("2026-10-22T12:00").toISOString() });
  });
  it("rejects blank reasons, past deadlines and deadlines beyond 30 days", () => {
    expect(parseReplacementInput("2026-10-22T12:00", "  ", now)).toEqual({ error: "Informe o motivo da nova gravação." });
    expect(parseReplacementInput("2026-10-18T12:00", "Regravar", now)).toEqual({ error: "Escolha um prazo no futuro." });
    expect(parseReplacementInput("not-a-date", "Regravar", now)).toEqual({ error: "Escolha um prazo no futuro." });
    expect(parseReplacementInput("2026-12-25T12:00", "Regravar", now)).toEqual({ error: "O prazo pode ser de no máximo 30 dias." });
  });
  it("proposes a deadline three days ahead", () => {
    const proposed = new Date(defaultReplacementDeadline(now));
    expect(Math.round((proposed.getTime() - now.getTime()) / 3_600_000)).toBe(72);
  });
});

describe("ReplacementRequest", () => {
  beforeEach(() => { graphql.mockReset(); });

  async function open(onRequested = vi.fn()) {
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(ReplacementRequest, { inspectionId: "inspection-1", mediaId: "media-1", onRequested })); });
    await act(async () => { host.querySelector("button")?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    return { host, root, onRequested };
  }
  async function type(element: Element | null, value: string) {
    await act(async () => {
      const proto = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, "value")?.set?.call(element, value);
      element?.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }

  it("does not call the API without a reason", async () => {
    const { host, root } = await open();
    await act(async () => { host.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(graphql).not.toHaveBeenCalled();
    expect(host.textContent).toContain("Informe o motivo da nova gravação.");
    await act(async () => { root.unmount(); });
  });

  it("requests the replacement with the media, a reason and a mutation id", async () => {
    graphql.mockResolvedValueOnce({ requestVideoReplacement: { replacement: { id: "r1", responsibilityId: "x", status: "REQUESTED", deadlineAt: "2026-10-22T12:00:00Z" }, userErrors: [], clientMutationId: "m" } });
    const { host, root, onRequested } = await open();
    await type(host.querySelector("textarea"), "Gravar sem pessoas");
    await act(async () => { host.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(graphql).toHaveBeenCalledTimes(1);
    const input = graphql.mock.calls[0][1].input;
    expect(input).toMatchObject({ inspectionId: "inspection-1", mediaId: "media-1", reason: "Gravar sem pessoas" });
    expect(typeof input.clientMutationId).toBe("string");
    expect(onRequested).toHaveBeenCalledTimes(1);
    expect(host.textContent).toContain("Nova gravação solicitada.");
    await act(async () => { root.unmount(); });
  });

  it("shows validation errors returned by the API without notifying success", async () => {
    graphql.mockResolvedValueOnce({ requestVideoReplacement: { replacement: null, userErrors: [{ code: "INVALID_STATE", field: "inspectionId", message: "a new recording can only be requested for an inspection that needs attention" }], clientMutationId: "m" } });
    const { host, root, onRequested } = await open();
    await type(host.querySelector("textarea"), "Gravar sem pessoas");
    await act(async () => { host.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(host.querySelector("[role=alert]")?.textContent).toContain("needs attention");
    expect(onRequested).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
  });
});
