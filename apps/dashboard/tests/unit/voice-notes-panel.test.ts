import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { graphql } from "@/graphql/client";
import { VoiceNotesPanel } from "@/features/dashboard/voice-notes-panel";

vi.mock("@/graphql/client", async (importOriginal) => ({ ...await importOriginal<typeof import("@/graphql/client")>(), graphql: vi.fn() }));

describe("VoiceNotesPanel", () => {
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.mocked(graphql).mockReset();
  });

  it("keeps the inspection detail usable when the audioNotes connection is missing", async () => {
    vi.mocked(graphql).mockResolvedValueOnce({} as never);
    const host = document.createElement("div");
    const root = createRoot(host);

    await act(async () => {
      root.render(createElement(VoiceNotesPanel, { inspectionId: "inspection-a" }));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(host.textContent).toContain("Anotações de voz");
    expect(host.textContent).toContain("Não foi possível carregar as anotações de voz.");

    await act(async () => root.unmount());
  });
});
