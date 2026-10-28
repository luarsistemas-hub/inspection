import * as React from "react";
import { act, createElement, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { graphql, graphqlOptionalPermission } from "@/graphql/client";
import { VideoOperationsPage } from "@/features/admin/video-operations-page";

vi.mock("@/graphql/client", () => ({ graphql: vi.fn(), graphqlOptionalPermission: vi.fn() }));
vi.mock("@inspection/design-system", async () => {
  const React = await import("react");
  return {
    Button: ({ children, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) => React.createElement("button", props, children),
    Field: ({ label, children }: { label: string; children: ReactNode }) => React.createElement("label", null, label, children),
    Input: (props: InputHTMLAttributes<HTMLInputElement>) => React.createElement("input", props),
    Select: (props: SelectHTMLAttributes<HTMLSelectElement>) => React.createElement("select", props),
    Textarea: (props: TextareaHTMLAttributes<HTMLTextAreaElement>) => React.createElement("textarea", props),
  };
});

describe("VideoOperationsPage", () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

  it("UT-363 renders sampled, retained and representative frame counts on the operations page", async () => {
    vi.stubGlobal("React", React);
    vi.mocked(graphql).mockImplementation(async (document) => {
      const operation = (document as unknown as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
      if (operation === "AdminVideoPreparations") return { videoPreparations: { nodes: [{ preparationId: "prep-a", mediaId: "media-a", status: "READY", stage: "CONCLUÍDA", attempts: 1, processedFrames: 80, framesSampled: 80, framesRetained: 24, framesRepresentative: 6, retryable: false, sourceAvailable: true, preparationVersion: 2, errorCode: "" }], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "AdminSensitiveContentScreeningStatus") return { sensitiveContentScreeningBypassed: false } as never;
      throw new Error(`Unexpected operation ${operation}`);
    });
    vi.mocked(graphqlOptionalPermission).mockResolvedValue({ videoAdmission: null } as never);
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoOperationsPage)); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(host.querySelector("article[aria-label='Preparação prep-a']")?.textContent).toContain("Quadros: 80 amostrados · 24 mantidos · 6 enviados à análise");
    await act(async () => { root.unmount(); });
  });
});
